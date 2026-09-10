package main

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// testIssueModel is testModel for the issue list: no tracker, no
// fetches; the children's arguments are recorded.
func testIssueModel(t *testing.T) (model, *[]string) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	var mu sync.Mutex
	var calls []string
	m := newIssueModel(defaultConfig(), "acme/example", nil, nil)
	m.cfg.OnOpen = "quit" // pinned, as in testModel
	m.noInit = true
	m.runSelf = func(kind string, args ...string) error {
		mu.Lock()
		calls = append(calls, kind+" "+strings.Join(args, " "))
		mu.Unlock()
		<-done
		return errors.New("test ended")
	}
	return m, &calls
}

func mkIssue(key, title, branch, state, stype string, prio int, age time.Duration) Issue {
	is := Issue{Key: key, Title: title, Branch: branch, Priority: prio, UpdatedAt: time.Now().Add(-age)}
	is.State.Name, is.State.Type = state, stype
	is.Team.Key = "BAR"
	return is
}

// withProject is the fixture's issue inside a project; BAR-4159 stays
// outside one, as issues filed straight into the team do.
func withProject(is Issue, name string) Issue {
	is.Project.Name = name
	return is
}

func fixtureIssues() []Issue {
	return []Issue{
		mkIssue("BAR-4159", "Company fuzzy match accepts particle-only overlap", "bar-4159-company-fuzzy-match", "In Review", "started", 2, 8*time.Hour),
		withProject(mkIssue("BAR-4160", "Per-tenant captureEngine override", "bar-4160-per-tenant-override", "In Progress", "started", 2, 2*time.Hour), "Sequential Capture"),
		withProject(mkIssue("BAR-4578", "Step A — deterministic validator", "bar-4578-step-a", "Todo", "unstarted", 1, 6*24*time.Hour), "Sequential Capture"),
		withProject(mkIssue("BAR-4404", "Shadow output validation", "bar-4404-shadow", "Backlog", "backlog", 0, 9*24*time.Hour), "Endpoint Validation"),
	}
}

// fixtureDone is an issue completed inside doneWindow and one closed
// two days ago — the second must not reach the list.
func fixtureDone() []Issue {
	fresh := withProject(mkIssue("BAR-4286", "Open update-activity fields", "bar-4286-open-update", "Done", "completed", 0, 3*time.Hour), "Endpoint Validation")
	fresh.CompletedAt = time.Now().Add(-3 * time.Hour)
	stale := mkIssue("BAR-4109", "Direct assignment strands calculation", "bar-4109-direct-assignment", "Done", "completed", 0, 48*time.Hour)
	stale.CompletedAt = time.Now().Add(-48 * time.Hour)
	return []Issue{fresh, stale}
}

func TestIssueListRendersSectionsAndBadges(t *testing.T) {
	m, _ := testIssueModel(t)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 30))

	approved := PR{Number: 3543, HeadRefName: "bar-4159-company-fuzzy-match"}
	r := Review{State: "APPROVED", SubmittedAt: time.Now().UTC().Format(time.RFC3339)}
	r.Author.Login = "alice"
	approved.Reviews = []Review{r}
	draft := PR{Number: 3550, HeadRefName: "bar-4578-step-a", IsDraft: true}
	// A second PR on BAR-4159, from a branch nobody named after Linear's
	// slug: both chips show, newest first.
	second := PR{Number: 3561, HeadRefName: "fix/bar-4159-particle-guard"}
	tm.Send(issuesMsg{issues: fixtureIssues()})
	tm.Send(issuePRsMsg{prs: []PR{approved, draft, second}})
	tm.Send(doneMsg{issues: fixtureDone()})
	tm.Send(localMsg{"bar-4160-per-tenant-override": {Worktree: "/wt/bar-4160-per-tenant-override", Window: "bar-4160-per-tenant-override", ClaudeState: agentBlocked}})
	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)
	out := string(readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second))))

	for _, want := range []string{
		"owl · issues · acme/example",
		"In progress", "Todo", "Backlog",
		"Done", "· 1d", // the window the Done section reaches back
		"BAR-4160", "⎇", "©", // the workspace and the blocked agent
		"BAR-4159", "!!", "8h", "In Review", "#3561  #3543✓",
		"BAR-4578", "!!!", "#3550 draft",
		"BAR-4404", "Shadow output validation",
		"Sequential Capture", "Endpoint Validati", // the project column, trimmed
		"BAR-4286", // completed three hours ago
		"4 open · 2 in progress · 1 todo · 1 backlog",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("issue list lacks %q:\n%s", want, out)
		}
	}
	// Closed two days ago: past doneWindow, so it stays out however the
	// tracker (or a stale cache) answered.
	if strings.Contains(out, "BAR-4109") {
		t.Errorf("an issue done 48h ago is in the list:\n%s", out)
	}
	// The state name only where it adds something: "In Review" inside In
	// progress yes, "Backlog" inside Backlog no.
	if strings.Count(out, "Backlog") != 1 || strings.Count(out, "Todo") != 1 {
		t.Errorf("a row repeats its section's state name:\n%s", out)
	}
	// Newest change first within a section: BAR-4160 (2h) above BAR-4159 (8h).
	if strings.Index(out, "BAR-4160") > strings.Index(out, "BAR-4159") {
		t.Errorf("sections are not newest-first:\n%s", out)
	}
	if strings.Contains(out, "check feedback") {
		t.Errorf("the issue list offers the review's feedback key:\n%s", out)
	}
}

func TestIssueListFiltersByKeyOrTitle(t *testing.T) {
	m, _ := testIssueModel(t)
	m.issues = fixtureIssues()
	m.ready = true
	m.width, m.height = 140, 30
	m.resizeViewport()

	m.search.SetValue("shadow")
	rows := m.visibleRows()
	if len(rows) != 2 || rows[1].issue == nil || rows[1].issue.Key != "BAR-4404" {
		t.Errorf("filter by title: %+v", rows)
	}
	m.search.SetValue("bar-41")
	var keys []string
	for _, r := range m.visibleRows() {
		if r.issue != nil {
			keys = append(keys, r.issue.Key)
		}
	}
	if strings.Join(keys, " ") != "BAR-4160 BAR-4159" {
		t.Errorf("filter by key: %v", keys)
	}
	// A project fragment narrows to that project, across sections.
	m.search.SetValue("sequential")
	keys = nil
	for _, r := range m.visibleRows() {
		if r.issue != nil {
			keys = append(keys, r.issue.Key)
		}
	}
	if strings.Join(keys, " ") != "BAR-4160 BAR-4578" {
		t.Errorf("filter by project: %v", keys)
	}
}

func TestIssueListEnterOpensTheFeature(t *testing.T) {
	m, calls := testIssueModel(t)
	m.issues = fixtureIssues()
	m.ready = true
	m.cfg.OnOpen = "stay"
	m.width, m.height = 140, 30
	m.resizeViewport()
	m.clampCursor() // the first issue row: BAR-4160, newest in progress

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	nm := next.(model)
	if cmd == nil || nm.inflight["BAR-4160"] != "opening BAR-4160…" {
		t.Fatalf("Enter did not launch: inflight %v", nm.inflight)
	}
	runBatch(cmd)
	time.Sleep(100 * time.Millisecond)
	// The child runs in the issue scope: `owl issue open`, not `owl pr open`.
	if len(*calls) != 1 || (*calls)[0] != "issue open BAR-4160" {
		t.Errorf("child args %v, want [issue open BAR-4160]", *calls)
	}
	// f is `when: conversation`: with nothing started for this issue it
	// does nothing rather than starting a workspace to talk to.
	nm.inflight = map[string]string{}
	next, cmd = nm.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if cmd != nil || len(next.(model).inflight) != 0 {
		t.Error("f started a workspace for an issue that had none")
	}
	// With one, it sends the review prompt to the agent already there.
	nm.localState = map[string]LocalState{"bar-4160-per-tenant-override": {Worktree: "/wt", Window: "bar-4160-per-tenant-override"}}
	next, cmd = nm.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if cmd == nil || next.(model).inflight["BAR-4160"] == "" {
		t.Errorf("f did not reach the running agent: %v", next.(model).inflight)
	}
	runBatch(cmd)
	time.Sleep(100 * time.Millisecond)
	last := (*calls)[len(*calls)-1]
	if !strings.HasPrefix(last, "issue start BAR-4160 --prompt ") || !strings.Contains(last, "failure mode") {
		t.Errorf("f ran %q", last)
	}
	// c only fires with a workspace to remove. (inflight is a map, so
	// the f above is still in the one nm holds; a second key on a row
	// is refused while one is in flight.)
	nm.inflight = map[string]string{}
	nm.localState = map[string]LocalState{"bar-4160-per-tenant-override": {Worktree: "/wt"}}
	next, cmd = nm.Update(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if cmd == nil || next.(model).inflight["BAR-4160"] != "closing BAR-4160…" {
		t.Errorf("c did not launch close: %v", next.(model).inflight)
	}
}

func TestIssueCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if loadIssueCache() != nil {
		t.Fatal("a cache from nowhere")
	}
	m := newIssueModel(defaultConfig(), "acme/example", nil, nil)
	m.issues, m.doneIssues, m.ready, m.cursor = fixtureIssues(), fixtureDone(), true, 2
	m.issuePRs = byIssueKey([]PR{{Number: 7, HeadRefName: "bar-4159-company-fuzzy-match"}})
	m.lastFetched = time.Now()
	m.persistCache()
	resumed := newIssueModel(defaultConfig(), "acme/example", nil, loadIssueCache())
	if len(resumed.issues) != 4 || len(resumed.issuePRs["BAR-4159"]) != 1 || resumed.issuePRs["BAR-4159"][0].Number != 7 || !resumed.ready || resumed.cursor != 2 {
		t.Errorf("resumed = %d issues, prs %v, ready %v, cursor %d", len(resumed.issues), resumed.issuePRs, resumed.ready, resumed.cursor)
	}
	// The done ones survive the round trip with their completedAt, and
	// the window still keeps the stale one off the list.
	if len(resumed.doneIssues) != 2 || resumed.doneIssues[0].CompletedAt.IsZero() {
		t.Errorf("resumed done = %+v", resumed.doneIssues)
	}
	resumed.width, resumed.height = 140, 30
	var keys []string
	for _, r := range resumed.visibleIssueRows() {
		if r.issue != nil {
			keys = append(keys, r.issue.Key)
		}
	}
	if strings.Contains(strings.Join(keys, " "), "BAR-4109") {
		t.Errorf("a cached issue done 48h ago reached the rows: %v", keys)
	}
}

// runBatch runs a command the way the runtime would: a batch's
// members each in their own goroutine, anything else as is.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					go c()
				}
			}
		}
	}()
}

func TestIssueListLegendNamesFeatures(t *testing.T) {
	m := newIssueModel(defaultConfig(), "acme/app", nil, nil)
	var short []string
	for _, b := range m.keys.ShortHelp() {
		if b.Enabled() { // the footer hides disabled bindings
			short = append(short, b.Help().Desc)
		}
	}
	legend := strings.Join(short, " • ")
	for _, want := range []string{"open feature", "open issue in browser"} {
		if !strings.Contains(legend, want) {
			t.Errorf("legend lacks %q: %s", want, legend)
		}
	}
	for _, stale := range []string{"open review", "open PR in browser", "feedback"} {
		if strings.Contains(legend, stale) {
			t.Errorf("legend still says %q: %s", stale, legend)
		}
	}
}

// An issue binding hands its prompt, with the row's key, through start.
func TestIssueBindingHandsThePrompt(t *testing.T) {
	cfg, err := parseConfig([]byte("bindings:\n  issue:\n    - {key: p, name: Continue, prompt: \"Continue {key}\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	m, calls := testIssueModel(t)
	m.cfg = cfg
	m.keys = newKeyMap(cfg.Keys, cfg.Bindings.Issue)
	m.issues = fixtureIssues()
	m.ready = true
	m.cfg.OnOpen = "stay"
	m.width, m.height = 140, 30
	m.resizeViewport()
	m.clampCursor()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	nm := next.(model)
	if cmd == nil || nm.inflight["BAR-4160"] != "Continue on BAR-4160…" {
		t.Fatalf("p did not launch: inflight %v", nm.inflight)
	}
	runBatch(cmd)
	time.Sleep(100 * time.Millisecond)
	if len(*calls) != 1 || (*calls)[0] != "issue start BAR-4160 --prompt Continue BAR-4160" {
		t.Errorf("child args %v", *calls)
	}
	// f is no issue key unless bound there: nothing launches.
	nm.inflight = map[string]string{}
	if _, cmd := nm.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}); cmd != nil {
		t.Error("f launched something on the issue list")
	}
}

// TestIssueListShowsWhatWasCancelled: cancelling is not finishing, so
// it gets a section of its own rather than a row in Done — and a
// window of its own, wider than Done's, because a cancellation is
// usually someone else's decision about your work.
func TestIssueListShowsWhatWasCancelled(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dropped := func(key, title string, ago time.Duration) Issue {
		is := mkIssue(key, title, "bar-x", "Canceled", "canceled", 2, ago)
		is.CanceledAt = time.Now().Add(-ago)
		return is
	}
	m := newIssueModel(defaultConfig(), "acme/example", nil, nil)
	m.issues, m.ready = fixtureIssues(), true
	m.cancelledIssues = []Issue{
		dropped("BAR-4287", "drop the shadow validation", 12*time.Hour),
		// Past the window: the query bounds it, and a cache written days
		// ago would otherwise smuggle this one back in.
		dropped("BAR-1724", "the one from last week", 8*24*time.Hour),
	}
	m.width, m.height = 160, 40
	m.resizeViewport()

	var b strings.Builder
	for _, row := range m.visibleIssueRows() {
		b.WriteString(m.renderRow(row, false) + "\n")
	}
	out := stripANSI(b.String())

	if !strings.Contains(out, "Canceled") || !strings.Contains(out, "3d") {
		t.Errorf("no Cancelled section:\n%s", out)
	}
	if !strings.Contains(out, "BAR-4287") {
		t.Errorf("an issue cancelled 12h ago is missing:\n%s", out)
	}
	if strings.Contains(out, "BAR-1724") {
		t.Errorf("an issue cancelled eight days ago is in the list:\n%s", out)
	}
	// The section is named what Linear calls the state, so the row does
	// not say it a second time in the state column.
	if strings.Count(out, "Canceled") != 1 {
		t.Errorf("the cancelled row repeats its section's state name:\n%s", out)
	}
	// Its own section, below Done rather than mixed into it.
	lines := strings.Split(out, "\n")
	done, cancelled := -1, -1
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "Done"):
			done = i
		case strings.HasPrefix(l, "Canceled"):
			cancelled = i
		}
	}
	if cancelled == -1 {
		t.Fatalf("no Canceled heading:\n%s", out)
	}
	if done != -1 && cancelled < done {
		t.Errorf("Canceled sits above Done: %d, %d", cancelled, done)
	}
}
