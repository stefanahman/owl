// UI tests via charmbracelet/x/exp/teatest/v2 — exercise the bubbletea
// program without a real TTY. Fetches are suppressed by setting
// initCmds to an empty slice; tests then Send synthetic msgs
// (prsMsg, mergedMsg, localMsg, userMsg) and assert on the
// rendered View output.
//
// These tests complement smoke_test.go, which exercises the data
// layer against a real gh/git/tmux environment.
package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// newTestModel returns a model with the shell-out Init cmds suppressed
// and the terminal size fixed. Tests can then Send synthetic messages.
func newTestModel(t *testing.T) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, testModel(t), teatest.WithInitialTermSize(120, 30))
}

// testModel is a model that never shells out: no Init fetches, and a
// runSelf that blocks until the test ends instead of executing the
// test binary as `pr-owl open`.
func testModel(t *testing.T) model {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	m := initialModel(defaultConfig())
	m.initCmds = []tea.Cmd{} // suppress fetchPRs/fetchLocal/etc
	m.me = "stefanahman"     // stable login for MyReviewStatus derivation
	m.repo = "acme/example"  // deterministic header regardless of cwd
	m.runSelf = func(args ...string) (string, error) {
		<-done
		return "", errors.New("test ended")
	}
	return m
}

// fixturePRs returns a small stable set covering all three status
// groups (Todo / Waiting / Approved), for a single-repo view.
func fixturePRs() []PR {
	now := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)

	mkReview := func(login, state string) Review {
		r := Review{State: state, SubmittedAt: now}
		r.Author.Login = login
		return r
	}
	mkPR := func(n int, title, author string, reviews []Review) PR {
		p := PR{Number: n, Title: title, UpdatedAt: now, Reviews: reviews}
		p.Author.Login = author
		return p
	}

	return []PR{
		// Todo: no review from me
		mkPR(4116, "refactor(desktop): tighten agent surface", "mr-sandstorm", nil),

		// Waiting: I requested changes
		mkPR(4114, "fix(report): return the assigned emission factor", "jefftrinidad29",
			[]Review{mkReview("stefanahman", "CHANGES_REQUESTED")}),

		// Approved: I approved
		mkPR(4076, "fix(capture): invalidate freight data", "iggerask",
			[]Review{mkReview("stefanahman", "APPROVED")}),

		// Waiting: I commented (folds into Waiting on author since COMMENTED
		// is feedback given, ball's in author's court)
		mkPR(4085, "feat(pipeline): streaming local", "guerillacoder",
			[]Review{mkReview("stefanahman", "COMMENTED")}),
	}
}

func fixtureMerged() []PR {
	now := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	p := PR{Number: 4070, Title: "chore(deps): bump lockfile", UpdatedAt: now, MergedAt: now}
	p.Author.Login = "dependabot"
	return []PR{p}
}

// readAll drains the reader once — fine for FinalOutput which returns
// a fully-buffered stream.
func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	return out
}

// mustQuit sends the Quit key so FinalOutput returns; keeps every test
// symmetric in shape.
func mustQuit(tm *teatest.TestModel) {
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
}

// TestRendersGroupSections verifies each of Todo/Waiting/Approved
// gets its own section header when PRs in that group exist.
func TestRendersGroupSections(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	// Give the update loop a beat to render.
	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	for _, want := range []string{"Todo", "Waiting for author", "Approved"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("output missing section header %q\n---\n%s", want, out)
		}
	}
	if bytes.Contains(out, []byte("Other")) {
		t.Errorf("Other group should no longer appear (COMMENTED folds into Waiting on author)\n---\n%s", out)
	}
	for _, want := range []string{"#4116", "#4114", "#4076", "#4085"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("output missing PR number %q\n---\n%s", want, out)
		}
	}
}

// TestMergedSection verifies the merged group renders when merged PRs
// are supplied.
func TestMergedSection(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(fixtureMerged()))

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	if !bytes.Contains(out, []byte("Merged (last")) {
		t.Errorf("output missing merged section header\n---\n%s", out)
	}
	if !bytes.Contains(out, []byte("#4070")) {
		t.Errorf("output missing merged PR #4070\n---\n%s", out)
	}
}

// TestSearchFilters verifies typing a PR number narrows the visible rows.
// Assertions inspect the final model state (cleaner than parsing the
// terminal output buffer, which contains every intermediate render).
func TestSearchFilters(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	// Give initial render a beat.
	time.Sleep(100 * time.Millisecond)

	// Enter search mode + type "4116", then blur so `q` quits.
	tm.Type("/4116")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if got := m.search.Value(); got != "4116" {
		t.Fatalf("search value = %q, want %q", got, "4116")
	}
	rows := m.visibleRows()
	seenPRs := map[int]bool{}
	for _, r := range rows {
		if r.pr != nil {
			seenPRs[r.pr.Number] = true
		}
	}
	if !seenPRs[4116] {
		t.Errorf("filtered rows missing #4116; seen=%v", seenPRs)
	}
	for _, gone := range []int{4114, 4076, 4085} {
		if seenPRs[gone] {
			t.Errorf("filtered rows still contain non-matching #%d; seen=%v", gone, seenPRs)
		}
	}
}

// TestApprovedBadgeRenders locks in the #4074 fix: when I approved
// the PR (even if I later commented on it), the ✓ badge appears.
// Non-approved-but-engaged PRs get · instead.
func TestApprovedBadgeRenders(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))
	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	// #4076 in fixturePRs has my APPROVED review; ✓ must render on it.
	if !bytes.Contains(out, []byte("✓")) {
		t.Errorf("expected ✓ badge somewhere in output (fixture includes an approved PR)\n---\n%s", out)
	}
	// The fixture's #4114 has my CHANGES_REQUESTED (engaged, not approved) —
	// dim · should show for it.
	if !bytes.Contains(out, []byte("·")) {
		t.Errorf("expected · badge somewhere in output (fixture includes an engaged-but-not-approved PR)\n---\n%s", out)
	}
}

// TestCleanupGuardSkipsPRWithoutLocal verifies the guard at handleKey:
// pressing `c` on a PR with no worktree AND no tmux session doesn't
// fire pr-review-done (which would fail with "no such worktree" and
// bubble up as an errMsg).
func TestCleanupGuardSkipsPRWithoutLocal(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{}) // empty — no worktrees, no sessions
	tm.Send(mergedMsg(nil))
	time.Sleep(50 * time.Millisecond)

	// Press `c` on the currently-selected row (first Todo PR).
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	time.Sleep(50 * time.Millisecond)

	mustQuit(tm)
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if m.err != nil {
		t.Errorf("guard should have prevented cleanup, but got err = %v", m.err)
	}
}

// TestSearchRejectsNonDigits verifies the textinput Validate closure
// keeps non-digit input out of the search buffer.
func TestSearchRejectsNonDigits(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))
	time.Sleep(50 * time.Millisecond)

	// Enter search mode + type a letter (should be rejected by Validate).
	tm.Type("/abc")
	time.Sleep(50 * time.Millisecond)

	// Can't send `q` — search input is focused, `q` would land in it.
	// Direct Quit works from any state.
	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if got := m.search.Value(); got != "" {
		t.Errorf("expected empty search value after typing letters, got %q", got)
	}
}

// TestHelpModalOpensAndDismisses verifies `?` opens the modal, and
// any non-quit key closes it.
func TestHelpModalOpensAndDismisses(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))
	time.Sleep(50 * time.Millisecond)

	// Open modal
	tm.Send(tea.KeyPressMsg{Code: '?', Text: "?"})
	time.Sleep(50 * time.Millisecond)

	// Dismiss with a benign key (space)
	tm.Send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	time.Sleep(50 * time.Millisecond)

	mustQuit(tm)
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if m.showHelp {
		t.Errorf("modal should be dismissed, but showHelp is still true")
	}
}

// TestHelpModalQuitBypassesDismiss verifies `q` in the modal quits
// instead of just dismissing.
func TestHelpModalQuitBypassesDismiss(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))
	time.Sleep(50 * time.Millisecond)

	tm.Send(tea.KeyPressMsg{Code: '?', Text: "?"})
	time.Sleep(50 * time.Millisecond)

	// `q` should quit even from the modal
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})

	// If Quit worked, FinalModel returns without needing another mustQuit.
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	if _, ok := fm.(model); !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
}

// TestFocusRestoresSearch verifies that a FocusMsg re-focuses the
// search input when a filter is still active but the input was blurred
// (typical: user searched, hit Enter, switched away, came back).
func TestFocusRestoresSearch(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	// Type a search then blur it (Enter). Search value persists,
	// input loses focus.
	tm.Type("/4116")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	time.Sleep(50 * time.Millisecond)

	// Window regains focus.
	tm.Send(tea.FocusMsg{})
	time.Sleep(50 * time.Millisecond)

	// FinalModel needs Quit; but search is now focused, so `q` would
	// type into it. tm.Quit() calls tea.Quit directly, bypassing key
	// handling.
	if err := tm.Quit(); err != nil {
		t.Fatalf("quit: %v", err)
	}
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if m.search.Value() != "4116" {
		t.Fatalf("search value = %q, want 4116 (should persist)", m.search.Value())
	}
	if !m.search.Focused() {
		t.Errorf("expected search to be re-focused after FocusMsg with active filter")
	}
}

// TestFocusIgnoredWithoutFilter verifies FocusMsg is a noop when no
// filter is active — no unexpected focus change.
func TestFocusIgnoredWithoutFilter(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg(fixturePRs()))
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	tm.Send(tea.FocusMsg{})
	time.Sleep(50 * time.Millisecond)

	mustQuit(tm)
	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if m.search.Focused() {
		t.Errorf("search should not gain focus from FocusMsg when no filter is active")
	}
}

// TestEmptyState verifies the friendly message when there are no PRs.
func TestEmptyState(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{})
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	if !bytes.Contains(out, []byte("no PRs need your review")) {
		t.Errorf("empty state message missing\n---\n%s", out)
	}
}

// TestHeaderShowsRepo verifies the header renders the repo name.
func TestHeaderShowsRepo(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{})
	tm.Send(localMsg{})
	tm.Send(mergedMsg(nil))

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	if !bytes.Contains(out, []byte("acme/example")) {
		t.Errorf("header missing repo name\n---\n%s", out)
	}
	if !bytes.Contains(out, []byte("pr-owl")) {
		t.Errorf("header missing app name\n---\n%s", out)
	}
}

// ------------------------------------------------------------
// Direct-Update unit tests — cheaper than teatest for pure logic.
// ------------------------------------------------------------

func TestMyReviewStatusDerivation(t *testing.T) {
	me := "stefanahman"
	mkReview := func(login, state, submittedAt string) Review {
		r := Review{State: state, SubmittedAt: submittedAt}
		r.Author.Login = login
		return r
	}
	// Chronological timestamps — the derivation depends on order.
	t0 := "2026-08-27T10:00:00Z"
	t1 := "2026-08-27T11:00:00Z"
	t2 := "2026-08-28T09:00:00Z"
	t3 := "2026-08-28T10:00:00Z"

	cases := []struct {
		name    string
		reviews []Review
		want    ReviewStatus
	}{
		{"no reviews at all → Todo", nil, StatusTodo},
		{"someone else reviewed → Todo (I haven't)",
			[]Review{mkReview("bob", "APPROVED", t0)}, StatusTodo},
		{"I approved → Approved",
			[]Review{mkReview(me, "APPROVED", t0)}, StatusApproved},
		{"I requested changes → Waiting for author",
			[]Review{mkReview(me, "CHANGES_REQUESTED", t0)}, StatusWaitingForAuthor},
		{"I only commented → Waiting for author",
			[]Review{mkReview(me, "COMMENTED", t0)}, StatusWaitingForAuthor},
		{"my review dismissed → Todo",
			[]Review{mkReview(me, "DISMISSED", t0)}, StatusTodo},
		// #4074 shape — approved then follow-up comment. No new commits
		// since (no OIDs set → no staleness). Verdict stays APPROVED
		// because COMMENTED is skipped when picking my verdict.
		{"I approved then commented → still Approved",
			[]Review{
				mkReview(me, "CHANGES_REQUESTED", t0),
				mkReview(me, "COMMENTED", t1),
				mkReview(me, "APPROVED", t2),
				mkReview(me, "COMMENTED", t3),
			}, StatusApproved},
		{"I approved then requested changes → Waiting for author",
			[]Review{
				mkReview(me, "APPROVED", t0),
				mkReview(me, "CHANGES_REQUESTED", t2),
			}, StatusWaitingForAuthor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := PR{Reviews: tc.reviews}
			if got := pr.MyReviewStatus(me); got != tc.want {
				t.Errorf("MyReviewStatus = %v, want %v", got, tc.want)
			}
		})
	}

	// Staleness cases — Commit.OID + HeadRefOid populated, so the
	// stale check fires. Anchored on the #4141 shape: I engaged, author
	// pushed after, my review's commit != head.
	stale := []struct {
		name       string
		reviews    []Review
		headRefOid string
		want       ReviewStatus
	}{
		{
			name: "COMMENTED on old commit, author pushed → Waiting for you",
			reviews: []Review{
				func() Review { r := mkReview(me, "COMMENTED", t0); r.Commit.OID = "old"; return r }(),
			},
			headRefOid: "new",
			want:       StatusWaitingForYou,
		},
		{
			name: "APPROVED on old commit, author pushed → Waiting for you (stale approval)",
			reviews: []Review{
				func() Review { r := mkReview(me, "APPROVED", t0); r.Commit.OID = "old"; return r }(),
			},
			headRefOid: "new",
			want:       StatusWaitingForYou,
		},
		{
			name: "APPROVED on current commit → Approved",
			reviews: []Review{
				func() Review { r := mkReview(me, "APPROVED", t0); r.Commit.OID = "head"; return r }(),
			},
			headRefOid: "head",
			want:       StatusApproved,
		},
		{
			name: "COMMENTED on current commit → Waiting for author",
			reviews: []Review{
				func() Review { r := mkReview(me, "COMMENTED", t0); r.Commit.OID = "head"; return r }(),
			},
			headRefOid: "head",
			want:       StatusWaitingForAuthor,
		},
	}
	for _, tc := range stale {
		t.Run(tc.name, func(t *testing.T) {
			pr := PR{Reviews: tc.reviews, HeadRefOid: tc.headRefOid}
			if got := pr.MyReviewStatus(me); got != tc.want {
				t.Errorf("MyReviewStatus = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTrimCountsRunes(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"exactly-ten", 11, "exactly-ten"},
		{"fix(report) — return factor", 14, "fix(report) —…"},
		{"héllo wörld", 6, "héllo…"},
		{"🦉🦉🦉🦉", 2, "🦉…"},
		{"abc", 1, "a"},
		{"abc", 0, ""},
	}
	for _, tc := range cases {
		if got := trim(tc.in, tc.n); got != tc.want {
			t.Errorf("trim(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
		}
	}
}

func TestFindLocalForPR(t *testing.T) {
	state := map[string]LocalState{
		"pr-4116":               {Worktree: "/wt/pr-4116"},
		"pr-4115-fix-something": {Worktree: "/wt/pr-4115-fix-something", Session: "pr-4115-fix-something", ClaudeState: "done"},
		"bar-4098-unrelated":    {Worktree: "/wt/bar-4098-unrelated"},
	}

	if ls := findLocalForPR(state, 4116); ls.Worktree == "" {
		t.Errorf("expected exact pr-4116 match, got zero LocalState")
	}
	if ls := findLocalForPR(state, 4115); ls.Session != "pr-4115-fix-something" {
		t.Errorf("expected prefix match on pr-4115-*, got session=%q", ls.Session)
	}
	if ls := findLocalForPR(state, 4098); ls.Worktree != "" {
		t.Errorf("expected no match for pr-4098 (only bar-4098-* branch present), got wt=%q", ls.Worktree)
	}
}

// TestBusyState covers the open/close child lifecycle without spawning
// one: while busy, keys other than quit are ignored; a failed child
// surfaces its error and unblocks; a successful open quits the popup.
func TestBusyState(t *testing.T) {
	m := testModel(t)
	m.prs = fixturePRs()
	m.prsReady = true
	m.me = "stefanahman"
	m.refreshList()
	m.cursor = m.firstPRRowIndex()
	m.busy = "opening #4116…"

	next, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if got := next.(model); got.cursor != m.cursor {
		t.Errorf("cursor moved while busy: %d → %d", m.cursor, got.cursor)
	}
	if !strings.Contains(m.actionRowView(), "opening #4116…") {
		t.Errorf("action row should show the busy label, got %q", m.actionRowView())
	}

	failed, cmd := m.Update(openedMsg{err: errors.New("fetch failed")})
	if fm := failed.(model); fm.busy != "" || fm.err == nil {
		t.Errorf("after a failed open: busy=%q err=%v", fm.busy, fm.err)
	}
	if cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Error("a failed open must not quit the popup")
		}
	}

	opened, cmd := m.Update(openedMsg{out: "started =pr-reviews:=pr-4116"})
	if cmd == nil {
		t.Fatal("a successful open should quit")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("a successful open should quit the popup")
	}
	if om := opened.(model); om.farewell != "started =pr-reviews:=pr-4116" {
		t.Errorf("farewell = %q", om.farewell)
	}

	closed, cmd := m.Update(closedMsg{})
	if cm := closed.(model); cm.busy != "" || cmd == nil {
		t.Errorf("after close: busy=%q, refresh cmd=%v", cm.busy, cmd)
	}

	// on_open: stay keeps the TUI and refreshes instead of quitting.
	m.cfg.OnOpen = "stay"
	stayed, cmd := m.Update(openedMsg{out: "selected"})
	if sm := stayed.(model); sm.busy != "" || sm.farewell != "" || cmd == nil {
		t.Errorf("on_open stay: busy=%q farewell=%q cmd=%v", sm.busy, sm.farewell, cmd)
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Error("on_open stay must not quit")
	}
}

// TestGoldenFrames snapshots the full program output for the states
// the screen test (e2e/) can't reach without a live backend: loading,
// empty, a search filter, an error, and a running open. Refresh with
// `go test -run TestGoldenFrames -update .` and review the diff.
func TestGoldenFrames(t *testing.T) {
	frames := map[string]func(tm *teatest.TestModel){
		"loading": func(tm *teatest.TestModel) {},
		"empty": func(tm *teatest.TestModel) {
			tm.Send(prsMsg{})
			tm.Send(mergedMsg(nil))
		},
		"sections": func(tm *teatest.TestModel) {
			tm.Send(prsMsg(fixturePRs()))
			tm.Send(localMsg{"pr-4116-feat": {Worktree: "/wt", Session: "pr-4116-feat", ClaudeState: "blocked"}})
			tm.Send(mergedMsg(fixtureMerged()))
		},
		"search": func(tm *teatest.TestModel) {
			tm.Send(prsMsg(fixturePRs()))
			tm.Send(mergedMsg(nil))
			tm.Send(tea.KeyPressMsg{Code: '/', Text: "/"})
			tm.Send(tea.KeyPressMsg{Code: '4', Text: "4"})
			tm.Send(tea.KeyPressMsg{Code: '1', Text: "1"})
		},
		"error": func(tm *teatest.TestModel) {
			tm.Send(errMsg{errors.New("gh api graphql: HTTP 401: Bad credentials")})
		},
		"busy": func(tm *teatest.TestModel) {
			tm.Send(prsMsg(fixturePRs()))
			tm.Send(mergedMsg(nil))
			tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
		},
	}
	for name, drive := range frames {
		t.Run(name, func(t *testing.T) {
			tm := teatest.NewTestModel(t, testModel(t),
				teatest.WithInitialTermSize(100, 24),
				teatest.WithProgramOptions(tea.WithColorProfile(colorprofile.ANSI256)),
			)
			drive(tm)
			time.Sleep(150 * time.Millisecond)
			if err := tm.Quit(); err != nil {
				t.Fatal(err)
			}
			teatest.RequireEqualOutput(t, readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second))))
		})
	}
}
