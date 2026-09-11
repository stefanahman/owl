package main

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func ownPR(n int, decision, ci, mergeable string, asked int, draft bool) PR {
	return PR{
		Number: n, Title: "a change", HeadRefName: "mine/x",
		UpdatedAt:      time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		ReviewDecision: decision, CI: ci, Mergeable: mergeable, Asked: asked, IsDraft: draft,
	}
}

func TestWhyBlocked(t *testing.T) {
	for _, c := range []struct {
		name string
		pr   PR
		want blocking
	}{
		{"red check", ownPR(1, "REVIEW_REQUIRED", "FAILURE", "UNKNOWN", 0, false), blockedOnYou},
		{"errored check", ownPR(2, "REVIEW_REQUIRED", "ERROR", "UNKNOWN", 0, false), blockedOnYou},
		{"changes requested", ownPR(3, "CHANGES_REQUESTED", "SUCCESS", "MERGEABLE", 1, false), blockedOnYou},
		// Approved and green, and still yours to deal with: a conflict
		// outranks the approval, which is why the switch checks it first.
		{"approved but conflicting", ownPR(4, "APPROVED", "SUCCESS", "CONFLICTING", 0, false), blockedOnYou},
		{"approved and green", ownPR(5, "APPROVED", "SUCCESS", "MERGEABLE", 0, false), readyToMerge},
		// A draft is never ready to merge, however green.
		{"approved draft", ownPR(6, "APPROVED", "SUCCESS", "MERGEABLE", 0, true), notOutForReview},
		{"reviewers asked", ownPR(7, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 2, false), waitingOnOthers},
		// Marked ready with nobody asked and a draft nobody asked about
		// are the same state — never offered to anyone — so one section
		// holds both.
		{"ready but unasked", ownPR(8, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 0, false), notOutForReview},
		{"draft and unasked", ownPR(11, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 0, true), notOutForReview},
		// A draft with reviewers asked has been offered, so it is waiting
		// on them like any other.
		{"draft with reviewers asked", ownPR(12, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 2, true), waitingOnOthers},
		// Mergeable is UNKNOWN until GitHub is asked, so it must never be
		// the reason something lands in a section.
		{"unknown mergeability is not a conflict", ownPR(9, "APPROVED", "SUCCESS", "UNKNOWN", 0, false), readyToMerge},
		// Nothing has reported: no checks is not a failure.
		{"no checks at all", ownPR(10, "APPROVED", "", "MERGEABLE", 0, false), readyToMerge},
	} {
		if got := whyBlocked(c.pr); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDraftsSinkOnlyWhereTheyAreMixed(t *testing.T) {
	age := func(pr PR, d time.Duration) PR {
		pr.UpdatedAt = time.Now().Add(-d).UTC().Format(time.RFC3339)
		return pr
	}
	m := newModel(defaultConfig(), "acme/app", nil)
	m.mine = []PR{
		// Not out for review: the draft changed an hour ago and the one
		// marked ready two days ago, and the ready one still comes first.
		age(ownPR(1, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 0, true), time.Hour),
		age(ownPR(2, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 0, false), 48*time.Hour),
		// Waiting on reviewers, where a draft is ordinary: recency alone.
		age(ownPR(3, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 2, false), 72*time.Hour),
		age(ownPR(4, "REVIEW_REQUIRED", "SUCCESS", "UNKNOWN", 2, true), 2*time.Hour),
	}

	var order []int
	for _, row := range m.visibleMineRows() {
		if row.pr != nil {
			order = append(order, row.pr.Number)
		}
	}
	want := []int{4, 3, 2, 1}
	if len(order) != len(want) {
		t.Fatalf("rows = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("rows = %v, want %v", order, want)
		}
	}
}

func TestSplitPaneHeights(t *testing.T) {
	for _, c := range []struct {
		avail, want, other int
		wantH, otherH      int
	}{
		{20, 5, 8, 5, 8},   // both fit: each gets what it asked for
		{10, 3, 20, 3, 7},  // one is small: the other takes the rest
		{10, 20, 3, 7, 3},  // and the other way round
		{10, 20, 20, 5, 5}, // both want more than there is: split it
		{2, 9, 9, 1, 1},    // nothing to give: neither is squeezed to zero
	} {
		h, o := splitPaneHeights(c.avail, c.want, c.other)
		if h != c.wantH || o != c.otherH {
			t.Errorf("split(%d, %d, %d) = %d, %d; want %d, %d", c.avail, c.want, c.other, h, o, c.wantH, c.otherH)
		}
		if h+o > c.avail {
			t.Errorf("split(%d, %d, %d) overflowed: %d + %d", c.avail, c.want, c.other, h, o)
		}
	}
}

func TestPaneFocusSwapsCursorAndKeys(t *testing.T) {
	m := newModel(defaultConfig(), "acme/app", nil)
	m.me = "me"
	m.prs = []PR{{Number: 1, Title: "theirs", HeadRefName: "a"}}
	m.mine = []PR{ownPR(9, "APPROVED", "SUCCESS", "MERGEABLE", 0, false)}
	m.width, m.height, m.ready = 140, 30, true
	m.resizeViewport()

	if !m.panes() {
		t.Fatal("the PR list should have two panes")
	}
	// The review pane has the cursor, and the keys are the PR list's.
	if m.mineFocus {
		t.Error("the mine pane starts focused")
	}
	if row, _ := m.selectedRow(); row.mine {
		t.Error("the cursor starts in the mine pane")
	}
	if len(m.bindings()) == 0 || m.bindings()[0].Name != "check feedback" {
		t.Errorf("review pane bindings = %+v", m.bindings())
	}

	// Move the cursor, then Tab: the other pane gets the keys, and this
	// one keeps its place for when you come back.
	m.cursor = 1
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	nm := next.(model)
	if !nm.mineFocus {
		t.Fatal("tab did not move the focus")
	}
	if row, ok := nm.selectedRow(); !ok || !row.mine {
		t.Errorf("the cursor is not in the mine pane: %+v", row)
	}
	if len(nm.bindings()) == 0 || nm.bindings()[0].Name != "check the review" {
		t.Errorf("mine pane bindings = %+v", nm.bindings())
	}
	if !strings.Contains(nm.keys.Enter.Help().Desc, "your PR") {
		t.Errorf("the help still describes the other pane: %q", nm.keys.Enter.Help().Desc)
	}

	// And back, to the row we left.
	back, _ := nm.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	bm := back.(model)
	if bm.mineFocus || bm.cursor != 1 {
		t.Errorf("coming back: focus %v, cursor %d, want false, 1", bm.mineFocus, bm.cursor)
	}
}

func TestInactivePaneIsDimAndPlain(t *testing.T) {
	// Dimming text that already carries colour means taking the colour
	// off first, or the escape codes fight and the pane stays bright.
	coloured := styleApproved.Render("✓") + " " + styleChangesReqd.Render("⚠")
	dimmed := dimBlock(coloured)
	if strings.Contains(stripANSI(dimmed), "✓ ⚠") == false {
		t.Errorf("dimming lost the text: %q", stripANSI(dimmed))
	}
	if strings.Contains(dimmed, "42") || strings.Contains(dimmed, "203") {
		t.Errorf("the original colours survived dimming: %q", dimmed)
	}
	// Every line of a block, not just the first.
	two := dimBlock("one\ntwo")
	if lines := strings.Split(two, "\n"); len(lines) != 2 || lines[1] == "two" {
		t.Errorf("second line undimmed: %q", two)
	}
}

func TestPanesDoNotMoveWhenFocusDoes(t *testing.T) {
	// The two panes exist to save eye movement, so a pane that changed
	// place on Tab would cost more than it saved.
	m := newModel(defaultConfig(), "acme/app", nil)
	m.me = "me"
	m.prs = []PR{{Number: 1, Title: "theirs", HeadRefName: "a"}, {Number: 2, Title: "also theirs", HeadRefName: "b"}}
	m.mine = []PR{
		ownPR(9, "APPROVED", "SUCCESS", "MERGEABLE", 0, false),
		ownPR(8, "REVIEW_REQUIRED", "FAILURE", "UNKNOWN", 0, false),
	}
	m.width, m.height, m.ready = 140, 30, true
	m.resizeViewport()

	headings := func() (review, mine int, body string) {
		m.clampCursor()
		m.refreshList()
		body = stripANSI(m.panesView())
		review, mine = -1, -1
		for i, l := range strings.Split(body, "\n") {
			switch {
			case strings.HasPrefix(l, "To review ("):
				review = i
			case strings.HasPrefix(l, "Mine ("):
				mine = i
			}
		}
		return review, mine, body
	}

	m.mineFocus, m.cursor = false, 0
	r1, n1, first := headings()
	m.mineFocus, m.cursor = true, 0
	r2, n2, second := headings()

	if r1 != r2 || n1 != n2 {
		t.Errorf("the headings moved on focus: review %d→%d, mine %d→%d", r1, r2, n1, n2)
	}
	if r1 == -1 || n1 == -1 {
		t.Fatalf("a heading is missing:\n%s", first)
	}
	if r1 >= n1 {
		t.Errorf("the review pane is not above yours: %d, %d", r1, n1)
	}
	// Same rows in the same places; only the cursor marker differs.
	// Trailing space is not part of that: the focused pane comes from a
	// viewport, which pads its lines to width, and the dimmed one does
	// not.
	rows := func(s string) []string {
		lines := strings.Split(strings.ReplaceAll(s, "▸", " "), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " ")
		}
		return lines
	}
	a, b := rows(first), rows(second)
	if len(a) != len(b) {
		t.Fatalf("different numbers of lines: %d and %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("line %d moved:\n  %q\n  %q", i, a[i], b[i])
		}
	}
}

// TestCheckTheReviewFiresOnYourOwnPR: `f` is gated on the work having
// been started, and that gate used to look the workspace up by
// `pr-<N>`. One of your own is named for its branch, so the lookup
// found nothing and the key did nothing at all — silently, which is
// the worst way for a key to fail.
func TestCheckTheReviewFiresOnYourOwnPR(t *testing.T) {
	m := newModel(defaultConfig(), "acme/app", nil)
	m.me = "me"
	m.repoDir = t.TempDir()
	mine := ownPR(4290, "REVIEW_REQUIRED", "SUCCESS", "MERGEABLE", 1, false)
	mine.HeadRefName = "bar-4157-drop-legacy-tenant-shim"
	m.mine = []PR{mine}
	m.prs = []PR{{Number: 1, Title: "theirs", HeadRefName: "a"}}
	m.width, m.height, m.ready = 140, 30, true
	m.resizeViewport()

	// Onto the mine pane and onto the row.
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = next.(model)
	m.cursor = 1
	row, ok := m.selectedRow()
	if !ok || !row.mine {
		t.Fatalf("not on a mine row: %+v", row)
	}

	f := m.bindings()[0]
	if f.When != whenConversation {
		t.Fatalf("the f binding is not gated on a conversation: %+v", f)
	}
	// Nothing started: the key is correctly a no-op.
	if m.started(row) {
		t.Error("a PR with no workspace reads as started")
	}
	// The workspace exists under the branch's name, not the PR's.
	m.localState = map[string]LocalState{
		"bar-4157-drop-legacy-tenant-shim": {
			Worktree: "/repo/.worktrees.local/bar-4157-drop-legacy-tenant-shim",
			Branch:   "bar-4157-drop-legacy-tenant-shim",
			Window:   "bar-4157-drop-legacy-tenant-shim",
		},
	}
	if !m.started(row) {
		t.Error("f does not fire on your own PR whose workspace is open")
	}
	// And pressing it launches something rather than falling through.
	before := len(m.inflight)
	next, cmd := m.press(f)
	if cmd == nil || len(next.(model).inflight) != before+1 {
		t.Errorf("press did nothing: inflight %d -> %d", before, len(next.(model).inflight))
	}
}

// TestFetchStatesKeepsTheWorktrees: a watch signal re-reads the agent
// states and nothing else. `git worktree list` is a process, a busy
// agent signals every few hundred milliseconds, and running it at that
// rate would be worse than the poll the watch replaces — so the
// worktrees carry over from the last full read.
func TestFetchStatesKeepsTheWorktrees(t *testing.T) {
	m := model{localState: map[string]LocalState{
		"pr-42-fix": {Worktree: "/repo/.worktrees.local/pr-42-fix", Branch: "pr-42-fix", Window: "pr-42-fix", ClaudeState: agentWorking},
		"pr-7-old":  {Worktree: "/repo/.worktrees.local/pr-7-old", Branch: "pr-7-old"},
		"pr-9-gone": {Window: "pr-9-gone", ClaudeState: agentIdle},
	}}
	// No multiplexer answers here, so every window is gone.
	got := withStates(m.localState, nil)
	if ls := got["pr-42-fix"]; ls.Worktree == "" || ls.Branch == "" {
		t.Errorf("the worktree was dropped on a states-only read: %+v", ls)
	}
	if ls := got["pr-42-fix"]; ls.Window != "" || ls.ClaudeState != "" {
		t.Errorf("a window that is gone survived: %+v", ls)
	}
	if _, ok := got["pr-7-old"]; !ok {
		t.Error("a worktree with no window was dropped")
	}
	// An entry that was only ever a window, whose window has gone, goes
	// with it rather than lingering as an empty row.
	if _, ok := got["pr-9-gone"]; ok {
		t.Error("a window-only entry outlived its window")
	}
}
