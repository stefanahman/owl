package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func stackPROn(n int, base, head string) stackPR {
	return stackPR{Number: n, Title: "pr " + head, BaseRefName: base, HeadRefName: head}
}

// forkedStackPRs is the shape of a stack seen in a production monorepo: it
// forks twice, and the second fork has a line of two above it.
//
//	1201 ─┬─ 1202 ─ 1203 ─ 1204 ─ 1205 ─┬─ 1206
//	      └─ 1211                       ├─ 1207
//	                                    ├─ 1208 ─ 1209
//	                                    └─ 1210
func forkedStackPRs() []stackPR {
	return []stackPR{
		stackPROn(1201, "main", "base"),
		stackPROn(1202, "base", "schema"),
		stackPROn(1203, "schema", "api"),
		stackPROn(1204, "api", "report"),
		stackPROn(1205, "report", "review"),
		stackPROn(1206, "review", "ui"),
		stackPROn(1207, "review", "pool"),
		stackPROn(1208, "review", "queue"),
		stackPROn(1209, "queue", "queue-run"),
		stackPROn(1210, "review", "export"),
		stackPROn(1211, "base", "levels"),
		stackPROn(1212, "main", "lone"),
	}
}

func numbersOf(prs []stackPR) []int {
	out := []int{}
	for _, p := range prs {
		out = append(out, p.Number)
	}
	return out
}

// TestStackLine: the line through a PR runs from the bottom up to its
// one tip, or up to where it forks, and then names every tip above.
func TestStackLine(t *testing.T) {
	g := newPRGraph("acme/app", "main", forkedStackPRs())
	cases := []struct {
		at        int
		prs, tips []int
	}{
		{1206, []int{1201, 1202, 1203, 1204, 1205, 1206}, nil},
		{1204, []int{1201, 1202, 1203, 1204, 1205}, []int{1206, 1207, 1209, 1210}},
		{1208, []int{1201, 1202, 1203, 1204, 1205, 1208, 1209}, nil},
		{1211, []int{1201, 1211}, nil},
		{1201, []int{1201}, []int{1206, 1207, 1209, 1210, 1211}},
		{1212, []int{1212}, nil},
	}
	for _, c := range cases {
		l, err := g.line(c.at)
		if err != nil {
			t.Fatalf("line(%d): %v", c.at, err)
		}
		if got := numbersOf(l.PRs); !slices.Equal(got, c.prs) {
			t.Errorf("line(%d) = %v, want %v", c.at, got, c.prs)
		}
		if got := numbersOf(l.Tips); !slices.Equal(got, append([]int{}, c.tips...)) {
			t.Errorf("line(%d) tips = %v, want %v", c.at, got, c.tips)
		}
		if l.Base != "main" || l.At != c.at {
			t.Errorf("line(%d): base %q at %d", c.at, l.Base, l.At)
		}
	}
	if l, _ := g.line(1212); l.stacked() {
		t.Error("a PR with nothing below or above counts as stacked")
	}
}

// TestStackLineIgnoresForks: a fork's branch is never a base in this
// repository, however it is named — a fork's PR from a branch called
// `improvement` is not what #1206 is built on, and nothing is built on
// it.
func TestStackLineIgnoresForks(t *testing.T) {
	fork := stackPROn(9, "main", "review")
	fork.IsCrossRepository = true
	g := newPRGraph("acme/app", "main", append(forkedStackPRs(), fork))
	l, err := g.line(1206)
	if err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(l.PRs); len(got) != 6 || got[4] != 1205 {
		t.Errorf("line(1206) = %v: the fork's PR got into it", got)
	}
	if up := g.above(9); len(up) != 0 {
		t.Errorf("above the fork's PR: %v", up)
	}
}

// TestStackLineRefusesAnAmbiguousBase: one branch may be the head of
// two pull requests into two bases, and then the layer below is not
// one PR.
func TestStackLineRefusesAnAmbiguousBase(t *testing.T) {
	g := newPRGraph("acme/app", "main", []stackPR{
		stackPROn(1, "main", "x"),
		stackPROn(2, "release", "x"),
		stackPROn(3, "x", "y"),
	})
	_, err := g.line(3)
	if err == nil || !strings.Contains(err.Error(), "#1 and #2") {
		t.Errorf("line(3) = %v, want an error naming #1 and #2", err)
	}
}

// TestStackLineEndsOnACycle: a base and a head that name each other
// end the walk instead of looping on it.
func TestStackLineEndsOnACycle(t *testing.T) {
	g := newPRGraph("acme/app", "main", []stackPR{
		stackPROn(1, "b", "a"),
		stackPROn(2, "a", "b"),
	})
	l, err := g.line(1)
	if err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(l.PRs); !slices.Equal(got, []int{2, 1}) {
		t.Errorf("line(1) = %v", got)
	}
}

func TestStackLineOfAClosedPR(t *testing.T) {
	g := newPRGraph("acme/app", "main", forkedStackPRs())
	if _, err := g.line(1); !errors.Is(err, errNotOpen) {
		t.Errorf("line(1) = %v, want errNotOpen", err)
	}
}

// TestTheTrunkEndsTheWalk: a pull request from the trunk itself — a
// release PR from main into production — is no layer under every PR
// into main, and does not fork into all of them either.
func TestTheTrunkEndsTheWalk(t *testing.T) {
	g := newPRGraph("acme/app", "main", []stackPR{
		stackPROn(100, "production", "main"),
		stackPROn(101, "main", "a"),
		stackPROn(102, "main", "b"),
		stackPROn(103, "a", "c"),
	})
	for n, want := range map[int][]int{100: {100}, 101: {101, 103}, 102: {102}} {
		l, err := g.line(n)
		if err != nil {
			t.Fatal(err)
		}
		if got := numbersOf(l.PRs); !slices.Equal(got, want) || len(l.Tips) != 0 {
			t.Errorf("line(%d) = %v tips %v, want %v", n, got, numbersOf(l.Tips), want)
		}
	}
	if l, _ := g.line(100); l.stacked() {
		t.Error("the release PR counts as stacked")
	}
}

// repoGraphPage is one page of the repository query's answer.
func repoGraphPage(t *testing.T, trunk string, prs []stackPR) string {
	t.Helper()
	var page struct {
		Data struct {
			Repository struct {
				DefaultBranchRef struct {
					Name string `json:"name"`
				} `json:"defaultBranchRef"`
				PullRequests struct {
					Nodes []stackPR `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"repository"`
		} `json:"data"`
	}
	page.Data.Repository.DefaultBranchRef.Name = trunk
	page.Data.Repository.PullRequests.Nodes = prs
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// fakeGHPages puts a gh on PATH that prints the pages one after the
// other, as `gh api --paginate` does, and records its arguments.
func fakeGHPages(t *testing.T, pages ...string) (argsFile string) {
	t.Helper()
	dir := t.TempDir()
	body := filepath.Join(dir, "pages")
	if err := os.WriteFile(body, []byte(strings.Join(pages, "")), 0o644); err != nil {
		t.Fatal(err)
	}
	argsFile = filepath.Join(dir, "args")
	fakeGHOnPath(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+argsFile+"'\ncat '"+body+"'\n")
	return argsFile
}

// TestRepoGraphReadsEveryPage: a busy repository has more open pull requests
// than one page holds, and a layer on the second page is still a layer.
func TestRepoGraphReadsEveryPage(t *testing.T) {
	prs := forkedStackPRs()
	argsFile := fakeGHPages(t, repoGraphPage(t, "main", prs[:5]), repoGraphPage(t, "main", prs[5:]))
	g, err := repoGraph(t.TempDir(), "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.prs) != len(prs) || g.trunk != "main" || g.repo != "acme/app" {
		t.Errorf("graph: %d PRs, trunk %q, repo %q", len(g.prs), g.trunk, g.repo)
	}
	args := strings.Split(strings.TrimSpace(string(mustRead(t, argsFile))), "\n")
	for _, want := range []string{"--paginate", "owner=acme", "name=app"} {
		if !slices.Contains(args, want) {
			t.Errorf("gh was not given %q: %v", want, args)
		}
	}
}

// stackRepo is a git repository whose remote names acme/app, and a
// fake gh that answers the forked stack, with the working directory in
// it and owl's state in a directory of the test's own.
func stackRepo(t *testing.T) Config {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	repo := t.TempDir()
	if _, err := git(repo, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "remote", "add", "origin", "https://github.com/acme/app"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	fakeGHPages(t, repoGraphPage(t, "main", forkedStackPRs()))
	return defaultConfig()
}

func TestRunStack(t *testing.T) {
	cfg := stackRepo(t)
	var out strings.Builder
	if err := runStack(cfg, []string{"1204"}, &out); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(os.Getenv("XDG_STATE_HOME"), "owl", "reviews", "acme", "app")
	for _, want := range []string{
		"acme/app, on main\n",
		"▸ #1204  pr report\n",
		"it forks above #1205, into 4 lines that end at:\n",
		"    #1209  pr queue-run\n",
		"notes: " + notes + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := runStack(cfg, []string{"--json", "1206"}, &out); err != nil {
		t.Fatal(err)
	}
	var got struct {
		At    int       `json:"at"`
		PRs   []stackPR `json:"prs"`
		Tips  []stackPR `json:"tips"`
		Notes string    `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("%v:\n%s", err, out.String())
	}
	if got.At != 1206 || len(got.PRs) != 6 || len(got.Tips) != 0 || got.Notes != notes {
		t.Errorf("json: at %d, %d PRs, %d tips, notes %q", got.At, len(got.PRs), len(got.Tips), got.Notes)
	}

	for _, args := range [][]string{nil, {"x"}, {"1", "2"}, {"1", "--bogus"}} {
		var ue usageError
		if err := runStack(cfg, args, &out); !errors.As(err, &ue) {
			t.Errorf("%v: %v, want a usage error", args, err)
		}
	}
}

// TestPrintLineSaysWhenTheBaseIsNotTheTrunk: a bottom on a branch no
// open pull request carries is a stack whose lower layer merged
// without its branch deleted, or was never a pull request at all.
func TestPrintLineSaysWhenTheBaseIsNotTheTrunk(t *testing.T) {
	g := newPRGraph("acme/app", "main", []stackPR{stackPROn(1, "spike", "a"), stackPROn(2, "a", "b")})
	l, err := g.line(2)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	printLine(&out, l)
	if !strings.HasPrefix(out.String(), "acme/app, on spike, which is not main and no open pull request's branch\n") {
		t.Errorf("output:\n%s", out.String())
	}
}

// searchGraphPage is one page of the scope search's answer.
func searchGraphPage(t *testing.T, repo, trunk string, prs []stackPR) string {
	t.Helper()
	type node struct {
		stackPR
		Repository struct {
			NameWithOwner    string `json:"nameWithOwner"`
			DefaultBranchRef struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"repository"`
	}
	var page struct {
		Data struct {
			Search struct {
				Nodes []node `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	for _, p := range prs {
		n := node{stackPR: p}
		n.Repository.NameWithOwner, n.Repository.DefaultBranchRef.Name = repo, trunk
		page.Data.Search.Nodes = append(page.Data.Search.Nodes, n)
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestScopeGraphsGroupByRepository: a list spanning owners asks once,
// and a #7 in one repository is not built on a branch of another.
func TestScopeGraphsGroupByRepository(t *testing.T) {
	argsFile := fakeGHPages(t,
		searchGraphPage(t, "acme/app", "main", forkedStackPRs()[:6]),
		searchGraphPage(t, "acme/tool", "trunk", []stackPR{stackPROn(7, "trunk", "base")}),
	)
	graphs, err := scopeGraphs("user:acme")
	if err != nil {
		t.Fatal(err)
	}
	if len(graphs) != 2 || graphs["acme/app"].trunk != "main" || graphs["acme/tool"].trunk != "trunk" {
		t.Fatalf("graphs: %v", graphs)
	}
	if up := graphs["acme/app"].above(1201); !slices.Equal(up, []int{1202}) {
		t.Errorf("above #1201 in acme/app: %v", up)
	}
	if up := graphs["acme/tool"].above(7); len(up) != 0 {
		t.Errorf("acme/tool's #7 has %v on it, from another repository", up)
	}
	args := strings.Split(strings.TrimSpace(string(mustRead(t, argsFile))), "\n")
	if !slices.Contains(args, "--paginate") || !slices.Contains(args, "q=user:acme is:pr is:open") {
		t.Errorf("gh args: %v", args)
	}
}

// stackModel is the PR list with the forked stack's graph, as if the
// stacks fetch had landed, and the rows given.
func stackModel(t *testing.T, prs ...PR) model {
	t.Helper()
	m := testModel(t)
	m.prs = prs
	m.ready = true
	m.width, m.height = 160, 30
	m.resizeViewport()
	next, _ := m.Update(stacksMsg{gen: m.fetchGen, graphs: map[string]*prGraph{
		"acme/example": newPRGraph("acme/example", "main", forkedStackPRs()),
	}})
	return next.(model)
}

func TestStackMark(t *testing.T) {
	row := func(n int, title string) PR {
		p := PR{Number: n, Title: title}
		p.Author.Login = "alice"
		return p
	}
	long := "feat(search): rank the middle results on a level that finds any provider's records"
	m := stackModel(t, row(1203, "pr 1203"), row(1206, long), row(1212, "pr 1212"))
	check := func(when string, m model) {
		t.Helper()
		raw := m.render()
		screen := stripANSI(raw)
		// The stack is named by its bottom, and coloured: the column is
		// what tells a layer from a lone PR at a glance.
		for _, label := range []string{"≡1201 3", "≡1201 6/6"} {
			if !strings.Contains(raw, styleStack.Render(label)) {
				t.Errorf("%s: no %q in the stack colour:\n%s", when, label, screen)
			}
		}
		// One column: every title starts where the others do, a lone
		// PR's included.
		col := map[int]bool{}
		for _, title := range []string{"pr 1203", "feat(search)", "pr 1212"} {
			for _, line := range strings.Split(screen, "\n") {
				if i := strings.Index(line, title); i >= 0 {
					col[len([]rune(line[:i]))] = true
				}
			}
		}
		if len(col) != 1 {
			t.Errorf("%s: titles start in columns %v:\n%s", when, col, screen)
		}
	}
	check("after the fetch", m)
	// A failed fetch keeps the graphs it had: offline, the rows still
	// know where they sit.
	failed, _ := m.Update(stacksMsg{gen: m.fetchGen})
	check("after a failed fetch", failed.(model))

	// The title takes what the window gives it: whole in a wide one, cut
	// in a narrow one.
	if screen := stripANSI(m.render()); !strings.Contains(screen, long+" ") {
		t.Errorf("a 160-column window cut the title:\n%s", screen)
	}
	m.width = 100
	m.resizeViewport()
	m.refreshList()
	if screen := stripANSI(m.render()); strings.Contains(screen, long) || !strings.Contains(screen, "…") {
		t.Errorf("a 100-column window did not cut the title:\n%s", screen)
	}
}

// TestNoStackColumnWithoutStacks: a list with no layer in it spends no
// width on the column.
func TestNoStackColumnWithoutStacks(t *testing.T) {
	m := stackModel(t, PR{Number: 1212, Title: "lone"})
	if m.stackCol != 0 || m.stackCell(&m.prs[0]) != "" {
		t.Errorf("column %d, cell %q", m.stackCol, m.stackCell(&m.prs[0]))
	}
}

// TestAMergedRowIsNoLayer: a pull request that merged is no layer,
// whatever a graph fetched before the merge still says. The merged
// fetch can land before the stacks one, and a merged row with a label
// wider than the open rows' column once made the padding negative and
// took the list down.
func TestAMergedRowIsNoLayer(t *testing.T) {
	m := testModel(t)
	m.ready = true
	m.width, m.height = 160, 30
	m.resizeViewport()
	m.stacks = map[string]*prGraph{"acme/example": newPRGraph("acme/example", "main", []stackPR{
		stackPROn(12, "main", "a"), stackPROn(13, "a", "b"), stackPROn(14, "a", "c"),
		stackPROn(1201, "main", "x"), stackPROn(1202, "x", "y"),
	})}
	m.prs = []PR{{Number: 12, Title: "small"}}
	landed := PR{Number: 1201, Title: "landed", MergedAt: time.Now().UTC().Format(time.RFC3339)}
	m.merged = []PR{landed}
	m.refreshList()
	screen := stripANSI(m.render())
	if strings.Contains(screen, "≡1201") {
		t.Errorf("the merged #1201 carries a stack label:\n%s", screen)
	}
	if !strings.Contains(screen, "≡12 1") {
		t.Errorf("the open #12 lost its label:\n%s", screen)
	}
}

func TestStackGraphsSurviveTheCache(t *testing.T) {
	m := stackModel(t)
	m.persistCache()
	c := loadCache(prCacheKey(m.cfg.PR, m.repo, m.here))
	if c == nil {
		t.Fatal("no cache written")
	}
	back := graphsFromCache(c.Stacks)
	if g := back["acme/example"]; g == nil || len(g.prs) != len(forkedStackPRs()) || g.trunk != "main" {
		t.Fatalf("graphs read back: %v", back)
	}
	if l, err := back["acme/example"].line(1206); err != nil || len(l.PRs) != 6 {
		t.Errorf("line(1206) from the cache: %v %v", l.numbers(), err)
	}
}

// TestStackColumnShowsTheStackReview: a stack review open on a line
// puts its own ⎇ © after the label of every layer it holds — #1201 too,
// beside that row's own review — and not on #1211, a layer of the same
// stack on another line. A closed one keeps its record and shows
// nothing, and then the column spends no width on badges.
func TestStackColumnShowsTheStackReview(t *testing.T) {
	row := func(n int) PR {
		p := PR{Number: n, Title: "pr " + strconv.Itoa(n)}
		p.Author.Login = "alice"
		return p
	}
	m := stackModel(t, row(1206), row(1202), row(1201), row(1211))
	m.stackRecords = []stackRecord{{name: "stack-ui", Repo: "acme/example", Tip: 1206,
		PRs: []int{1201, 1202, 1203, 1204, 1205, 1206}}}
	m.localState = map[string]LocalState{
		"stack-ui":      {Worktree: "/wt/stack-ui", Window: "stack-ui", ClaudeState: agentWorking},
		"pr-1201-rules": {Worktree: "/wt/pr-1201-rules"},
	}
	m.refreshList()
	// "≡1201 1" padded to "≡1201 6/6", the widest label, then the badges.
	stackBadges := styleStack.Render("≡1201 1") + "  " + " " + workspaceBadges(m.localState["stack-ui"], "")
	lines := map[int]string{}
	for _, line := range strings.Split(m.render(), "\n") {
		for _, n := range []int{1206, 1202, 1201, 1211} {
			if strings.Contains(stripANSI(line), "pr "+strconv.Itoa(n)) {
				lines[n] = line
			}
		}
	}
	if !strings.Contains(lines[1201], stackBadges) {
		t.Errorf("#1201 lacks the stack review's badges after its label:\n%q", stripANSI(lines[1201]))
	}
	if !strings.Contains(lines[1201], styleWorktree.Render("⎇")+" ") || strings.Count(stripANSI(lines[1201]), "⎇") != 2 {
		t.Errorf("#1201 should carry its own ⎇ and the stack review's: %q", stripANSI(lines[1201]))
	}
	for _, n := range []int{1206, 1202} {
		if strings.Count(stripANSI(lines[n]), "⎇") != 1 || !strings.Contains(lines[n], styleClaudeWorking.Render("©")) {
			t.Errorf("#%d lacks the stack review's badges: %q", n, stripANSI(lines[n]))
		}
	}
	if strings.Contains(stripANSI(lines[1211]), "⎇") || strings.Contains(stripANSI(lines[1211]), "©") {
		t.Errorf("#1211 is on another line and shows the review: %q", stripANSI(lines[1211]))
	}

	// Closed: the record stays, the worktree and the window are gone.
	m.localState = map[string]LocalState{}
	m.refreshList()
	if m.stackBadges || strings.Contains(stripANSI(m.render()), "⎇") {
		t.Errorf("a closed stack review still shows: badges=%v", m.stackBadges)
	}
}
