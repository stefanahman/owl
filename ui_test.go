// UI tests via charmbracelet/x/exp/teatest/v2 — exercise the bubbletea
// program without a real TTY. Init is a no-op (noInit); tests Send
// synthetic msgs (prsMsg, mergedMsg, localMsg, userMsg) and assert on
// the rendered View output.
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
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// newTestModel returns a model with the shell-out Init cmds suppressed
// and the terminal size fixed. Tests can then Send synthetic messages.
func newTestModel(t *testing.T) *teatest.TestModel {
	t.Helper()
	return teatest.NewTestModel(t, testModel(t), teatest.WithInitialTermSize(120, 30))
}

// testModel is a model that never touches the machine: built by
// newModel (no git, no cache read), no Init fetches, the cache written
// to a throwaway directory, and a runSelf that blocks until the test
// ends instead of executing the test binary as `owl open`.
func testModel(t *testing.T) model {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	m := newModel(defaultConfig(), "acme/example", nil)
	m.noInit = true
	m.me = "stefanahman" // stable login for MyReviewStatus derivation
	m.runSelf = func(args ...string) error {
		<-done
		return errors.New("test ended")
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
		mkPR(3543, "add billing migration", "alice", nil),

		// Waiting for author: I requested changes
		mkPR(3510, "retry on 429", "erin",
			[]Review{mkReview("stefanahman", "CHANGES_REQUESTED")}),

		// Approved: I approved
		mkPR(3502, "bump node to 22", "dave",
			[]Review{mkReview("stefanahman", "APPROVED")}),

		// Waiting for author: I commented — feedback given, the ball is in
		// the author's court
		mkPR(3550, "fix retry ordering", "bob",
			[]Review{mkReview("stefanahman", "COMMENTED")}),
	}
}

func fixtureMerged() []PR {
	now := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	p := PR{Number: 3488, Title: "remove legacy flag", UpdatedAt: now, MergedAt: now}
	p.Author.Login = "erin"
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

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})

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
	for _, want := range []string{"#3543", "#3510", "#3502", "#3550"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("output missing PR number %q\n---\n%s", want, out)
		}
	}
}

// TestMergedSection verifies the merged group renders when merged PRs
// are supplied.
func TestMergedSection(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{prs: fixtureMerged()})

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	if !bytes.Contains(out, []byte("Merged (last")) {
		t.Errorf("output missing merged section header\n---\n%s", out)
	}
	if !bytes.Contains(out, []byte("#3488")) {
		t.Errorf("output missing merged PR #3488\n---\n%s", out)
	}
}

// TestSearchFilters verifies typing a PR number narrows the visible rows.
// Assertions inspect the final model state (cleaner than parsing the
// terminal output buffer, which contains every intermediate render).
func TestSearchFilters(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})

	// Give initial render a beat.
	time.Sleep(100 * time.Millisecond)

	// Enter search mode + type "3543", then blur so `q` quits.
	tm.Type("/3543")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m, ok := fm.(model)
	if !ok {
		t.Fatalf("expected final model of type model, got %T", fm)
	}
	if got := m.search.Value(); got != "3543" {
		t.Fatalf("search value = %q, want %q", got, "3543")
	}
	rows := m.visibleRows()
	seenPRs := map[int]bool{}
	for _, r := range rows {
		if r.pr != nil {
			seenPRs[r.pr.Number] = true
		}
	}
	if !seenPRs[3543] {
		t.Errorf("filtered rows missing #3543; seen=%v", seenPRs)
	}
	for _, gone := range []int{3510, 3502, 3550} {
		if seenPRs[gone] {
			t.Errorf("filtered rows still contain non-matching #%d; seen=%v", gone, seenPRs)
		}
	}
}

// TestApprovedBadgeRenders: when I approved the PR (even if I later
// commented on it), the ✓ badge appears.
// Non-approved-but-engaged PRs get · instead.
func TestApprovedBadgeRenders(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	// #3502 in fixturePRs has my APPROVED review; ✓ must render on it.
	if !bytes.Contains(out, []byte("✓")) {
		t.Errorf("expected ✓ badge somewhere in output (fixture includes an approved PR)\n---\n%s", out)
	}
	// The fixture's #3510 has my CHANGES_REQUESTED (engaged, not approved) —
	// dim · should show for it.
	if !bytes.Contains(out, []byte("·")) {
		t.Errorf("expected · badge somewhere in output (fixture includes an engaged-but-not-approved PR)\n---\n%s", out)
	}
}

// TestStaleBadgeColour: the glyph says what I did, the colour whether
// it still covers the head — a review on an older commit renders in
// the stale style, whatever its verdict.
func TestStaleBadgeColour(t *testing.T) {
	cases := []struct {
		name                     string
		approved, engaged, stale bool
		want                     string
	}{
		{"approved, covers head", true, true, false, styleApproved.Render("✓")},
		{"approved, head moved", true, true, true, styleReviewStale.Render("✓")},
		{"engaged, covers head", false, true, false, styleDim.Render("·")},
		{"engaged, head moved", false, true, true, styleReviewStale.Render("·")},
		{"no review", false, false, false, " "},
	}
	for _, c := range cases {
		got := badges(LocalState{}, "", c.approved, c.engaged, c.stale, false)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: badges = %q, want it to contain %q", c.name, got, c.want)
		}
	}
	// A stale approval is what puts a PR under "Waiting for you": the
	// row's status is the badge's stale flag.
	pr := fixturePRs()[2] // #3502, approved
	pr.HeadRefOid = "b"
	pr.Reviews[0].Commit.OID = "a"
	if got := pr.MyReviewStatus("stefanahman"); got != StatusWaitingForYou {
		t.Errorf("approval on an older commit: status = %v, want waiting for you", got)
	}
}

// TestCleanupGuardSkipsPRWithoutLocal verifies the guard at handleKey:
// pressing `c` on a PR with no worktree AND no tmux session doesn't
// run close (which would find nothing and surface as a notice).
func TestCleanupGuardSkipsPRWithoutLocal(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{}) // empty — no worktrees, no sessions
	tm.Send(mergedMsg{})
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
	if m.err != nil || m.notice != nil {
		t.Errorf("guard should have prevented cleanup, but got err = %v, notice = %v", m.err, m.notice)
	}
}

// TestSearchRejectsNonDigits verifies handleKey drops non-digit input
// before it reaches the textinput.
func TestSearchRejectsNonDigits(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
	time.Sleep(50 * time.Millisecond)

	// Enter search mode + type letters (dropped before the input sees them).
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

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
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

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
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

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})

	// Type a search then blur it (Enter). Search value persists,
	// input loses focus.
	tm.Type("/3543")
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
	if m.search.Value() != "3543" {
		t.Fatalf("search value = %q, want 3543 (should persist)", m.search.Value())
	}
	if !m.search.Focused() {
		t.Errorf("expected search to be re-focused after FocusMsg with active filter")
	}
}

// TestFocusIgnoredWithoutFilter verifies FocusMsg is a noop when no
// filter is active — no unexpected focus change.
func TestFocusIgnoredWithoutFilter(t *testing.T) {
	tm := newTestModel(t)

	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})

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
	tm.Send(mergedMsg{})

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
	tm.Send(mergedMsg{})

	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)

	out := readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second)))

	if !bytes.Contains(out, []byte("acme/example")) {
		t.Errorf("header missing repo name\n---\n%s", out)
	}
	if !bytes.Contains(out, []byte("owl")) {
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
		// Approved, then a follow-up comment. No new commits since (no
		// OIDs set → no staleness). Verdict stays APPROVED
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
	// stale check fires: I engaged, the author pushed after, my review's
	// commit != head.
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
		"pr-3543":               {Worktree: "/wt/pr-3543"},
		"pr-3542-fix-something": {Worktree: "/wt/pr-3542-fix-something", Window: "pr-3542-fix-something", ClaudeState: "done"},
		"other-3498-unrelated":  {Worktree: "/wt/other-3498-unrelated"},
	}

	if ls := findLocalForPR(state, 3543); ls.Worktree == "" {
		t.Errorf("expected exact pr-3543 match, got zero LocalState")
	}
	if ls := findLocalForPR(state, 3542); ls.Window != "pr-3542-fix-something" {
		t.Errorf("expected prefix match on pr-3542-*, got window=%q", ls.Window)
	}
	if ls := findLocalForPR(state, 3498); ls.Worktree != "" {
		t.Errorf("expected no match for pr-3498 (only bar-3498-* branch present), got wt=%q", ls.Worktree)
	}
}

// TestChildrenRunInTheBackground: open and close children don't lock
// the list — keys keep working, the action row shows what is in
// flight, a second key on the same PR is refused until the child
// reports, a failure is a notice, success refreshes the overlay.
func TestChildrenRunInTheBackground(t *testing.T) {
	m := testModel(t)
	m.cfg.OnOpen = "stay"
	m.prs = fixturePRs()
	m.prsReady = true
	m.width, m.height = 100, 24
	m.resizeViewport()
	m.refreshList()
	m.cursor = m.firstPRRowIndex()

	started, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = started.(model)
	if cmd == nil || m.inflight[3543] == "" {
		t.Fatalf("Enter did not start an open: inflight=%v", m.inflight)
	}
	if !strings.Contains(m.actionRowView(), "opening #3543…") {
		t.Errorf("action row = %q", m.actionRowView())
	}
	if moved, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown}); moved.(model).cursor == m.cursor {
		t.Error("the cursor is stuck while a child runs")
	}
	again, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if am := again.(model); cmd != nil || am.notice == nil || !strings.Contains(am.notice.Error(), "still opening #3543") {
		t.Errorf("a second Enter on the same PR: cmd=%v notice=%v", cmd, am.notice)
	}

	failed, _ := m.Update(openedMsg{pr: 3543, err: errors.New("fetch failed")})
	if fm := failed.(model); len(fm.inflight) != 0 || fm.notice == nil {
		t.Errorf("after a failed open: inflight=%v notice=%v", fm.inflight, fm.notice)
	}

	m.inflight[3543] = "opening #3543…"
	opened, cmd := m.Update(openedMsg{pr: 3543})
	if om := opened.(model); len(om.inflight) != 0 || om.farewell != "" || cmd == nil {
		t.Errorf("on_open stay after a successful open: inflight=%v farewell=%q cmd=%v", om.inflight, om.farewell, cmd)
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Error("on_open stay must not quit")
	}

	m.inflight[3543] = "closing #3543…"
	closed, cmd := m.Update(closedMsg{pr: 3543})
	if cm := closed.(model); len(cm.inflight) != 0 || cmd == nil {
		t.Errorf("after close: inflight=%v refresh cmd=%v", cm.inflight, cmd)
	}
}

// TestStartStaysInTheList: s starts the workspace without on_open —
// even with quit configured — and the row carries the spinner in the
// worktree slot until the child reports.
func TestStartStaysInTheList(t *testing.T) {
	m := testModel(t) // on_open: quit by default
	m.prs = fixturePRs()
	m.prsReady = true
	m.width, m.height = 100, 24
	m.resizeViewport()
	m.refreshList()
	m.cursor = m.firstPRRowIndex()

	started, cmd := m.handleKey(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = started.(model)
	if cmd == nil || m.inflight[3543] != "starting #3543…" || m.farewell != "" {
		t.Fatalf("s: cmd=%v inflight=%v farewell=%q", cmd, m.inflight, m.farewell)
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Error("s must not quit, whatever on_open says")
	}
	m.refreshList()
	row := ""
	for _, line := range strings.Split(m.render(), "\n") {
		if strings.Contains(line, "#3543") {
			row = line
		}
	}
	if !strings.Contains(row, m.spinner.View()) || strings.Contains(row, "⎇") {
		t.Errorf("row while starting = %q, want the spinner in the worktree slot", row)
	}
	done, _ := m.Update(openedMsg{pr: 3543})
	m = done.(model)
	m.refreshList()
	if strings.Contains(m.render(), m.spinner.View()+"  ") && len(m.inflight) != 0 {
		t.Error("the spinner outlived the child")
	}

	// The overlay follows Claude's state on its own: a tick refetches
	// it and schedules the next.
	if _, cmd := m.Update(localTickMsg{}); cmd == nil {
		t.Error("a local tick should refetch and reschedule")
	}

	// f is the same kind of key: it stays in the list too.
	m.localState = map[string]LocalState{"pr-3543-feat": {Window: "pr-3543-feat"}}
	fed, cmd := m.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if fm := fed.(model); cmd == nil || fm.inflight[3543] != "sending feedback to #3543…" || fm.farewell != "" {
		t.Errorf("f: cmd=%v inflight=%v farewell=%q", cmd, fm.inflight, fm.farewell)
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Error("f must not quit")
	}
}

// TestOpenQuitsAtOnce: with on_open: quit the TUI ends the moment an
// open starts — the popup closes, the child finishes behind it.
func TestOpenQuitsAtOnce(t *testing.T) {
	tm := newTestModel(t)
	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
	time.Sleep(50 * time.Millisecond)

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(model)
	if fm.farewell != "opening #3543…" || fm.inflight[3543] == "" {
		t.Errorf("farewell=%q inflight=%v", fm.farewell, fm.inflight)
	}
}

// TestFrames drives the model through the states the screen test
// (e2e/) can't reach without a live backend — loading, empty, a search
// filter, an error, a running open — and asserts on what the final
// frame says. The e2e snapshots are the pixel-exact layer; this one
// only has to survive a renderer change.
func TestFrames(t *testing.T) {
	frames := map[string]struct {
		drive       func(tm *teatest.TestModel)
		want, avoid []string
	}{
		"loading": {
			drive: func(tm *teatest.TestModel) {},
			want:  []string{"loading PRs…"},
			avoid: []string{"Todo", "no PRs"},
		},
		"empty": {
			drive: func(tm *teatest.TestModel) {
				tm.Send(prsMsg{})
				tm.Send(mergedMsg{})
			},
			want:  []string{"no PRs need your review", "0 open"},
			avoid: []string{"Todo"},
		},
		"sections": {
			drive: func(tm *teatest.TestModel) {
				tm.Send(prsMsg{prs: fixturePRs()})
				tm.Send(localMsg{"pr-3543-feat": {Worktree: "/wt", Window: "pr-3543-feat", ClaudeState: "blocked"}})
				tm.Send(mergedMsg{prs: fixtureMerged()})
			},
			want: []string{"Todo", "Waiting for author", "Approved", "Merged (last 1d)",
				"#3543", "#3510", "#3502", "#3550", "#3488", "⎇", "©", "✓", "·",
				"4 open · 1 todo · 0 you · 2 author · 1 approved · 1 merged (1d)"},
		},
		"search": {
			drive: func(tm *teatest.TestModel) {
				tm.Send(prsMsg{prs: fixturePRs()})
				tm.Send(mergedMsg{})
				tm.Type("/354") // every fixture number starts with 35
			},
			want:  []string{"search /", "#3543"},
			avoid: []string{"#3550", "#3510", "#3502"},
		},
		"error": {
			drive: func(tm *teatest.TestModel) {
				tm.Send(errMsg{err: errors.New("gh api graphql: HTTP 401: Bad credentials")})
			},
			want:  []string{"error: gh api graphql: HTTP 401: Bad credentials"},
			avoid: []string{"loading"},
		},
		"busy": {
			drive: func(tm *teatest.TestModel) {
				tm.Send(prsMsg{prs: fixturePRs()})
				tm.Send(mergedMsg{})
				tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
			},
			want: []string{"opening #3543…", "#3543"},
		},
	}
	for name, f := range frames {
		t.Run(name, func(t *testing.T) {
			tm := newTestModel(t)
			f.drive(tm)
			time.Sleep(150 * time.Millisecond)
			if err := tm.Quit(); err != nil {
				t.Fatal(err)
			}
			frame := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second)).(model).render()
			for _, want := range f.want {
				if !strings.Contains(frame, want) {
					t.Errorf("frame lacks %q:\n%s", want, frame)
				}
			}
			for _, avoid := range f.avoid {
				if strings.Contains(frame, avoid) {
					t.Errorf("frame has %q:\n%s", avoid, frame)
				}
			}
		})
	}
}

// TestErrorsKeepTheList: a failed fetch leaves the last good list on
// screen and says so in the action row; an action's failure is a
// notice that the next key press clears; only with nothing to show is
// the error the body.
func TestErrorsKeepTheList(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 24
	m.resizeViewport()
	next, _ := m.Update(prsMsg{prs: fixturePRs()})
	m = next.(model)

	failed, _ := m.Update(errMsg{err: errors.New("gh api graphql: HTTP 502")})
	m = failed.(model)
	if !strings.Contains(m.render(), "#3543") {
		t.Error("a fetch failure hid the list")
	}
	if !strings.Contains(m.actionRowView(), "HTTP 502") {
		t.Errorf("action row = %q, want the fetch error", m.actionRowView())
	}
	ok, _ := m.Update(prsMsg{prs: fixturePRs()})
	if m = ok.(model); m.err != nil {
		t.Error("a successful fetch keeps the error")
	}

	noticed, _ := m.Update(noticeMsg{errors.New("open https://x: exit status 1")})
	m = noticed.(model)
	if !strings.Contains(m.actionRowView(), "exit status 1") {
		t.Errorf("action row = %q, want the notice", m.actionRowView())
	}
	pressed, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	if m = pressed.(model); m.notice != nil {
		t.Error("a key press keeps the notice")
	}

	empty := testModel(t)
	bare, _ := empty.Update(errMsg{err: errors.New("HTTP 401: Bad credentials")})
	if !strings.Contains(bare.(model).render(), "error: HTTP 401") {
		t.Error("with nothing to show, the error should be the body")
	}
}

// TestQuitWhileSearching: ctrl+c quits with the search input focused —
// it is a quit key that isn't text, unlike q, which the input owns.
func TestQuitWhileSearching(t *testing.T) {
	tm := newTestModel(t)
	tm.Send(prsMsg{prs: fixturePRs()})
	tm.Send(localMsg{})
	tm.Send(mergedMsg{})
	tm.Type("/35")
	time.Sleep(50 * time.Millisecond)

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	fm := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	if m := fm.(model); m.search.Value() != "35" {
		t.Errorf("search value = %q, want 35 (ctrl+c must not be typed)", m.search.Value())
	}
}

// TestStaleFetchIsIgnored: r starts a new round without blanking the
// list; a result from an older round is dropped so a slow fetch can't
// overwrite a newer one.
func TestStaleFetchIsIgnored(t *testing.T) {
	m := testModel(t)
	m.width, m.height = 100, 24
	m.resizeViewport()
	loaded, _ := m.Update(prsMsg{prs: fixturePRs()})
	m = loaded.(model)

	refreshed, _ := m.handleKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
	m = refreshed.(model)
	if !m.refreshing || len(m.prs) != len(fixturePRs()) || m.fetchGen != 1 {
		t.Errorf("after r: refreshing=%v prs=%d gen=%d", m.refreshing, len(m.prs), m.fetchGen)
	}
	if !strings.Contains(m.actionRowView(), "refreshing") {
		t.Errorf("action row = %q", m.actionRowView())
	}

	stale, _ := m.Update(prsMsg{gen: 0, prs: nil})
	if m = stale.(model); len(m.prs) == 0 || !m.refreshing {
		t.Error("a result from the previous round replaced the list")
	}
	fresh, _ := m.Update(prsMsg{gen: 1, prs: fixturePRs()[:1]})
	if m = fresh.(model); len(m.prs) != 1 || m.refreshing {
		t.Errorf("the current round's result was not applied: prs=%d refreshing=%v", len(m.prs), m.refreshing)
	}
}
