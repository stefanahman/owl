package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func stackPROn(n int, base, head string) stackPR {
	return stackPR{Number: n, Title: "pr " + head, BaseRefName: base, HeadRefName: head}
}

// cfiPRs is the shape bardo-system's CFI stack had on 2026-10-13: it
// forks twice, and the second fork has a line of two above it.
//
//	4969 ─┬─ 4970 ─ 4971 ─ 4972 ─ 4973 ─┬─ 4974
//	      └─ 5124                       ├─ 4984
//	                                    ├─ 4993 ─ 5013
//	                                    └─ 5065
func cfiPRs() []stackPR {
	return []stackPR{
		stackPROn(4969, "main", "rules"),
		stackPROn(4970, "rules", "gaps"),
		stackPROn(4971, "gaps", "run"),
		stackPROn(4972, "run", "footprint"),
		stackPROn(4973, "footprint", "improvement"),
		stackPROn(4974, "improvement", "desktop"),
		stackPROn(4984, "improvement", "connection"),
		stackPROn(4993, "improvement", "uncovered"),
		stackPROn(5013, "uncovered", "uncovered-run"),
		stackPROn(5065, "improvement", "goods"),
		stackPROn(5124, "rules", "levels"),
		stackPROn(3582, "main", "lone"),
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
	g := newPRGraph("acme/app", "main", cfiPRs())
	cases := []struct {
		at        int
		prs, tips []int
	}{
		{4974, []int{4969, 4970, 4971, 4972, 4973, 4974}, nil},
		{4972, []int{4969, 4970, 4971, 4972, 4973}, []int{4974, 4984, 5013, 5065}},
		{4993, []int{4969, 4970, 4971, 4972, 4973, 4993, 5013}, nil},
		{5124, []int{4969, 5124}, nil},
		{4969, []int{4969}, []int{4974, 4984, 5013, 5065, 5124}},
		{3582, []int{3582}, nil},
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
	if l, _ := g.line(3582); l.stacked() {
		t.Error("a PR with nothing below or above counts as stacked")
	}
}

// TestStackLineIgnoresForks: a fork's branch is never a base in this
// repository, however it is named — a fork's PR from a branch called
// `improvement` is not what #4974 is built on, and nothing is built on
// it.
func TestStackLineIgnoresForks(t *testing.T) {
	fork := stackPROn(9, "main", "improvement")
	fork.IsCrossRepository = true
	g := newPRGraph("acme/app", "main", append(cfiPRs(), fork))
	l, err := g.line(4974)
	if err != nil {
		t.Fatal(err)
	}
	if got := numbersOf(l.PRs); len(got) != 6 || got[4] != 4973 {
		t.Errorf("line(4974) = %v: the fork's PR got into it", got)
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
	g := newPRGraph("acme/app", "main", cfiPRs())
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

// TestRepoGraphReadsEveryPage: bardo-system has more open pull requests
// than one page holds, and a layer on the second page is still a layer.
func TestRepoGraphReadsEveryPage(t *testing.T) {
	prs := cfiPRs()
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
// fake gh that answers the CFI stack, with the working directory in
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
	fakeGHPages(t, repoGraphPage(t, "main", cfiPRs()))
	return defaultConfig()
}

func TestRunStack(t *testing.T) {
	cfg := stackRepo(t)
	var out strings.Builder
	if err := runStack(cfg, []string{"4972"}, &out); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(os.Getenv("XDG_STATE_HOME"), "owl", "reviews", "acme", "app")
	for _, want := range []string{
		"acme/app, on main\n",
		"▸ #4972  pr footprint\n",
		"it forks above #4973, into 4 lines that end at:\n",
		"    #5013  pr uncovered-run\n",
		"notes: " + notes + "\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}

	out.Reset()
	if err := runStack(cfg, []string{"--json", "4974"}, &out); err != nil {
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
	if got.At != 4974 || len(got.PRs) != 6 || len(got.Tips) != 0 || got.Notes != notes {
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
