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
	m.noInit = true
	m.runSelf = func(args ...string) error {
		mu.Lock()
		calls = append(calls, strings.Join(args, " "))
		mu.Unlock()
		<-done
		return errors.New("test ended")
	}
	return m, &calls
}

func fixtureIssues() []Issue {
	mk := func(key, title, branch, state, stype string, prio int, age time.Duration) Issue {
		is := Issue{Key: key, Title: title, Branch: branch, Priority: prio, UpdatedAt: time.Now().Add(-age)}
		is.State.Name, is.State.Type = state, stype
		is.Team.Key = "BAR"
		return is
	}
	return []Issue{
		mk("BAR-4159", "Company fuzzy match accepts particle-only overlap", "bar-4159-company-fuzzy-match", "In Review", "started", 2, 8*time.Hour),
		mk("BAR-4160", "Per-tenant captureEngine override", "bar-4160-per-tenant-override", "In Progress", "started", 2, 2*time.Hour),
		mk("BAR-4578", "Step A — deterministic validator", "bar-4578-step-a", "Todo", "unstarted", 1, 6*24*time.Hour),
		mk("BAR-4404", "Shadow output validation", "bar-4404-shadow", "Backlog", "backlog", 0, 9*24*time.Hour),
	}
}

func TestIssueListRendersSectionsAndBadges(t *testing.T) {
	m, _ := testIssueModel(t)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(140, 30))

	approved := PR{Number: 3543, HeadRefName: "bar-4159-company-fuzzy-match"}
	r := Review{State: "APPROVED", SubmittedAt: time.Now().UTC().Format(time.RFC3339)}
	r.Author.Login = "alice"
	approved.Reviews = []Review{r}
	draft := PR{Number: 3550, HeadRefName: "bar-4578-step-a", IsDraft: true}
	tm.Send(issuesMsg{issues: fixtureIssues()})
	tm.Send(branchPRsMsg{prs: []PR{approved, draft}})
	tm.Send(localMsg{"bar-4160-per-tenant-override": {Worktree: "/wt/bar-4160-per-tenant-override", Window: "bar-4160-per-tenant-override", ClaudeState: agentBlocked}})
	time.Sleep(100 * time.Millisecond)
	mustQuit(tm)
	out := string(readAll(t, tm.FinalOutput(t, teatest.WithFinalTimeout(2*time.Second))))

	for _, want := range []string{
		"owl · issues · acme/example",
		"In progress", "Todo", "Backlog",
		"BAR-4160", "⎇", "©", // the workspace and the blocked agent
		"BAR-4159", "!!", "8h", "In Review", "#3543✓",
		"BAR-4578", "!!!", "#3550 draft",
		"BAR-4404", "Shadow output validation",
		"4 open · 2 in progress · 1 todo · 1 backlog",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("issue list lacks %q:\n%s", want, out)
		}
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
	if len(*calls) != 1 || (*calls)[0] != "open BAR-4160" {
		t.Errorf("child args %v, want [open BAR-4160]", *calls)
	}
	// Feedback is not an issue key: nothing launches.
	nm.inflight = map[string]string{}
	next, cmd = nm.Update(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if cmd != nil || len(next.(model).inflight) != 0 {
		t.Error("f launched something on the issue list")
	}
	// c only fires with a workspace to remove.
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
	m.issues, m.branchPRs, m.ready, m.cursor = fixtureIssues(), byBranch([]PR{{Number: 7, HeadRefName: "bar-4159-company-fuzzy-match"}}), true, 2
	m.lastFetched = time.Now()
	m.persistCache()
	resumed := newIssueModel(defaultConfig(), "acme/example", nil, loadIssueCache())
	if len(resumed.issues) != 4 || resumed.branchPRs["bar-4159-company-fuzzy-match"].Number != 7 || !resumed.ready || resumed.cursor != 2 {
		t.Errorf("resumed = %d issues, prs %v, ready %v, cursor %d", len(resumed.issues), resumed.branchPRs, resumed.ready, resumed.cursor)
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
	if len(*calls) != 1 || (*calls)[0] != "start BAR-4160 --prompt Continue BAR-4160" {
		t.Errorf("child args %v", *calls)
	}
	// f is no issue key unless bound there: nothing launches.
	nm.inflight = map[string]string{}
	if _, cmd := nm.Update(tea.KeyPressMsg{Code: 'f', Text: "f"}); cmd != nil {
		t.Error("f launched something on the issue list")
	}
}
