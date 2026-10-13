package main

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestStackLayersDrawTheTree: the layers of every line through #4971,
// a fork's branches drawn as a tree, and #5124 — off #4969, on no line
// through #4971 — left out.
func TestStackLayersDrawTheTree(t *testing.T) {
	g := newPRGraph("acme/app", "main", cfiPRs())
	var got []string
	for _, lp := range stackLayers(g, 4971) {
		got = append(got, fmt.Sprintf("%s%d", lp.prefix, lp.n))
	}
	want := []string{"4969", "4970", "4971", "4972", "4973",
		"├─ 4974", "├─ 4984", "├─ 4993", "│  5013", "└─ 5065"}
	if !slices.Equal(got, want) {
		t.Errorf("layers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var tips []int
	for _, l := range stackLines(g, 4971) {
		tips = append(tips, l.tip().Number)
	}
	if !slices.Equal(tips, []int{4974, 4984, 5013, 5065}) {
		t.Errorf("lines end at %v", tips)
	}
	if lines := stackLines(g, 5124); len(lines) != 1 || lines[0].tip().Number != 5124 {
		t.Errorf("a tip's view has its one line: %v", lines)
	}
}

// stackViewModel is the PR list on #4971 with the CFI stack fetched,
// a runSelf that records what it was asked to run, and → pressed.
func stackViewModel(t *testing.T) (model, func() []string) {
	t.Helper()
	row := PR{Number: 4971, Title: "run a refinement run"}
	m := stackModel(t, row)
	m.cfg.OnOpen = "stay"
	var mu sync.Mutex
	var calls []string
	m.runSelf = func(kind string, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, kind+" "+strings.Join(args, " "))
		return nil
	}
	m.clampCursor()
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	return next.(model), func() []string {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

func TestStackView(t *testing.T) {
	m, _ := stackViewModel(t)
	if m.stack == nil || m.stack.at != 4971 {
		t.Fatalf("→ on #4971 opened no stack view: %+v", m.stack)
	}
	screen := stripANSI(m.render())
	for _, want := range []string{
		"the stack of #4971 · acme/example",
		"Stack review · 4 lines",
		"≡ #4969 → #4974",
		"≡ #4969 → #5013",
		"Layers",
		"│  pr uncovered-run",
		"└─ pr goods",
		"10 layers · 4 lines · on main",
	} {
		if !strings.Contains(screen, want) {
			t.Errorf("screen lacks %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "#5124") {
		t.Errorf("a layer on no line through #4971 is shown:\n%s", screen)
	}
	// The cursor lands on the first line: Enter there is the stack
	// review, which is what → was pressed for.
	if row, ok := m.selectedRow(); !ok || row.line == nil || row.line.tip().Number != 4974 {
		t.Errorf("cursor on %+v", row)
	}

	back, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	bm := back.(model)
	if bm.stack != nil || bm.selectedPR() == nil || bm.selectedPR().Number != 4971 {
		t.Errorf("← did not come back to #4971: stack=%v", bm.stack)
	}
}

// TestStackViewKeys: Enter and s on a line run its stack review, c
// closes it, Enter on a layer opens that layer's own review, and a
// binding refuses a line.
func TestStackViewKeys(t *testing.T) {
	m, calls := stackViewModel(t)

	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatch(cmd)
	m.moveCursor(1) // the line to #4984
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	runBatch(cmd)
	// c needs a workspace there to close.
	if err := writeStackRecordFor(t, &m, "stack-desktop", 4974); err != nil {
		t.Fatal(err)
	}
	m.cursor = m.firstPRRowIndex()
	m.inflight = map[string]string{} // the opens above have reported
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: 'c', Text: "c"})
	runBatch(cmd)
	// f, the shipped binding, is a layer's.
	if next, cmd := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"}); cmd != nil || next.(model).notice == nil {
		t.Errorf("f on a line: cmd=%v notice=%v", cmd, next.(model).notice)
	}
	m.inflight = map[string]string{}
	// The first layer, #4969, opens its own review.
	rows := m.visibleRows()
	for i, r := range rows {
		if r.pr != nil && r.line == nil {
			m.cursor = i
			break
		}
	}
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runBatch(cmd)

	want := []string{
		"pr open 4974 --stack",
		"pr start 4984 --stack",
		"pr close --stack 4974",
		"pr open 4969",
	}
	// runBatch runs each command on a goroutine of its own, so the
	// children report in any order.
	got := calls()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("children:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// writeStackRecordFor gives the line to tip a workspace with a window,
// as an open would have left it.
func writeStackRecordFor(t *testing.T, m *model, name string, tip int) error {
	t.Helper()
	g := m.stackGraph()
	l, err := g.line(tip)
	if err != nil {
		return err
	}
	m.stackRecords = []stackRecord{{name: name, Repo: g.repo, Tip: tip, PRs: l.numbers()}}
	m.localState = map[string]LocalState{name: {Window: name}}
	return nil
}

// TestStackViewLayersLand: the layers' details replace what the graph
// alone knew — your review and the checks — for the view they were
// fetched for, and not for another opened since.
func TestStackViewLayersLand(t *testing.T) {
	m, _ := stackViewModel(t)
	reviewed := PR{Number: 4970, Title: "pr gaps", CI: "FAILURE", Repo: "acme/example",
		HeadRefOid: "new", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	r := Review{State: "APPROVED", SubmittedAt: time.Now().UTC().Format(time.RFC3339)}
	r.Author.Login, r.Commit.OID = m.me, "old"
	reviewed.Reviews = []Review{r}

	other, _ := m.Update(stackLayersMsg{gen: m.fetchGen, repo: "acme/example", at: 4974, prs: []PR{reviewed}})
	if om := other.(model); len(om.stack.layers) != 0 {
		t.Error("layers fetched for another view were taken")
	}
	next, _ := m.Update(stackLayersMsg{gen: m.fetchGen, repo: "acme/example", at: 4971, prs: []PR{reviewed}})
	nm := next.(model)
	var line string
	for _, l := range strings.Split(nm.render(), "\n") {
		if strings.Contains(stripANSI(l), "#4970") {
			line = l
		}
	}
	if !strings.Contains(line, styleReviewStale.Render("✓")) || !strings.Contains(line, styleChangesReqd.Render("✗")) {
		t.Errorf("#4970's row lacks the stale approval and the failing checks: %q", stripANSI(line))
	}
}

// TestStackViewPutsTheFilterAside: the view does not filter, so it
// shows no filter, and ← brings the list back as it was left — the
// filter and the row under it.
func TestStackViewPutsTheFilterAside(t *testing.T) {
	m := stackModel(t, PR{Number: 4974, Title: "desktop"}, PR{Number: 4971, Title: "run"})
	m.search.SetValue("4971")
	m.clampCursor()
	in, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	im := in.(model)
	if im.stack == nil || im.search.Value() != "" || strings.Contains(stripANSI(im.actionRowView()), "filter") {
		t.Fatalf("in the view: stack=%v filter=%q action row %q", im.stack, im.search.Value(), stripANSI(im.actionRowView()))
	}
	out, _ := im.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	om := out.(model)
	if om.search.Value() != "4971" || om.selectedPR() == nil || om.selectedPR().Number != 4971 {
		t.Errorf("back: filter %q, row %v", om.search.Value(), om.selectedPR())
	}
}

// TestStackViewRefusesAnUnstackedRow: → on a row that is no layer says
// so and stays in the list.
func TestStackViewRefusesAnUnstackedRow(t *testing.T) {
	m := stackModel(t, PR{Number: 3582, Title: "lone"})
	m.clampCursor()
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	nm := next.(model)
	if nm.stack != nil || nm.notice == nil || !strings.Contains(nm.notice.Error(), "#3582 is not a layer of a stack") {
		t.Errorf("→ on a lone PR: stack=%v notice=%v", nm.stack, nm.notice)
	}
}

// ownStackModel is the CFI stack with #4971 yours, in the feature
// workspace its branch is checked out in, entered from the mine pane,
// with a review of someone else's #4970 open beside it.
func ownStackModel(t *testing.T) (model, func() []string) {
	t.Helper()
	prs := cfiPRs()
	for i := range prs {
		prs[i].Author.Login = "alice"
		if prs[i].Number == 4971 {
			prs[i].Author.Login = "stefanahman"
		}
	}
	m := testModel(t)
	m.cfg.OnOpen = "stay"
	var mu sync.Mutex
	var calls []string
	m.runSelf = func(kind string, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, kind+" "+strings.Join(args, " "))
		return nil
	}
	m.ready = true
	m.width, m.height = 160, 40
	m.resizeViewport()
	own := PR{Number: 4971, Title: "pr run", HeadRefName: "run"}
	own.Author.Login = "stefanahman"
	m.mine = []PR{own}
	m.stacks = map[string]*prGraph{"acme/example": newPRGraph("acme/example", "main", prs)}
	m.localState = map[string]LocalState{
		"bar-4157-run": {Worktree: "/wt/bar-4157-run", Branch: "run", Window: "bar-4157-run"},
		"pr-4970-gaps": {Worktree: "/wt/pr-4970-gaps", Branch: "pr-4970-gaps", Window: "pr-4970-gaps"},
	}
	m.mineFocus = true
	m.keys = newKeyMap(m.cfg.Keys, m.bindings())
	m.refreshList()
	m.clampCursor()
	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	return next.(model), func() []string {
		time.Sleep(100 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), calls...)
	}
}

// cursorOn puts the cursor on the layer row of PR n.
func cursorOn(t *testing.T, m *model, n int) visibleRow {
	t.Helper()
	for i, r := range m.visibleRows() {
		if r.pr != nil && r.line == nil && r.pr.Number == n {
			m.cursor = i
			return r
		}
	}
	t.Fatalf("no layer row for #%d", n)
	return visibleRow{}
}

// TestStackViewKeysFollowTheLayer: f on someone else's layer checks the
// feedback you gave, whichever pane the view was opened from, and on a
// layer of your own the review you were given; your own layer finds its
// feature workspace.
func TestStackViewKeysFollowTheLayer(t *testing.T) {
	m, calls := ownStackModel(t)
	if m.stack == nil {
		t.Fatal("→ from the mine pane opened no stack view")
	}
	row := cursorOn(t, &m, 4971)
	if !row.mine || m.localOf(row).Worktree != "/wt/bar-4157-run" {
		t.Errorf("your #4971 does not find its feature workspace: mine=%v local=%+v", row.mine, m.localOf(row))
	}
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	runBatch(cmd)
	m.inflight = map[string]string{}
	cursorOn(t, &m, 4970)
	_, cmd = m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	runBatch(cmd)

	got := calls()
	slices.Sort(got)
	if len(got) != 2 ||
		!strings.HasPrefix(got[0], "pr start 4970 --prompt Please carefully check the feedback since your last review") ||
		!strings.HasPrefix(got[1], "pr start 4971 --prompt Please go through the review on pull request 4971") {
		t.Errorf("f sent:\n%s", strings.Join(got, "\n"))
	}
}

// TestStackViewSurvivesARefresh: a refresh that closes layers keeps the
// cursor on a row; quitting from the view saves the list's row, not the
// view's; / does not offer a filter the view does not apply; and a view
// whose PR left its stack says so.
func TestStackViewSurvivesARefresh(t *testing.T) {
	m, _ := stackViewModel(t)
	m.cursor = len(m.visibleRows()) - 1 // the last layer, #5065
	shrunk := newPRGraph("acme/example", "main", cfiPRs()[:6])
	next, _ := m.Update(stacksMsg{gen: m.fetchGen, graphs: map[string]*prGraph{"acme/example": shrunk}})
	m = next.(model)
	if _, ok := m.selectedRow(); !ok {
		t.Errorf("after the view shrank the cursor is on no row: %d of %d", m.cursor, len(m.visibleRows()))
	}

	m.persistCache()
	if c := loadCache(prCacheKey(m.cfg.PR, m.repo, m.here)); c == nil || c.Cursor != m.stack.back {
		t.Errorf("the cache's cursor is the view's, not the list's row %d: %+v", m.stack.back, c)
	}

	searched, _ := m.handleKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if searched.(model).search.Focused() {
		t.Error("/ opened a filter the view does not apply")
	}

	gone, _ := m.Update(stacksMsg{gen: m.fetchGen, graphs: map[string]*prGraph{
		"acme/example": newPRGraph("acme/example", "main", []stackPR{stackPROn(3582, "main", "lone")}),
	}})
	if screen := stripANSI(gone.(model).render()); !strings.Contains(screen, "#4971 is no longer a layer of an open stack") {
		t.Errorf("an emptied view:\n%s", screen)
	}
}
