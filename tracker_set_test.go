package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// stubTracker is a workspace's worth of canned answers. The set does
// no HTTP of its own — it composes Trackers — so this is the level the
// merging and the routing are tested at.
type stubTracker struct {
	issues   []Issue
	projects []Project
	err      error // when set, every read fails with it
	created  []string
	team     string
	claim    Claimed  // what Claim answers
	claims   []string // the keys it was asked to claim
}

func (s *stubTracker) Issues() ([]Issue, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.issues, nil
}
func (s *stubTracker) Done(time.Time) ([]Issue, error)      { return s.Issues() }
func (s *stubTracker) Cancelled(time.Time) ([]Issue, error) { return s.Issues() }
func (s *stubTracker) Projects() ([]Project, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.projects, nil
}
func (s *stubTracker) DoneProjects(time.Time) ([]Project, error) { return s.Projects() }
func (s *stubTracker) Project(id string) (Project, error) {
	if s.err != nil {
		return Project{}, s.err
	}
	for _, p := range s.projects {
		if p.ID == id || p.SlugID == id {
			return p, nil
		}
	}
	// Linear answers a project of another workspace with an empty value
	// and no error, which is what makes tryEach need `found`.
	return Project{}, nil
}
func (s *stubTracker) ProjectIssues(id string) ([]Issue, error) {
	if s.err != nil {
		return nil, s.err
	}
	var out []Issue
	for _, i := range s.issues {
		if i.Project.ID == id {
			out = append(out, i)
		}
	}
	return out, nil
}
func (s *stubTracker) Issue(key string) (Issue, error) {
	if s.err != nil {
		return Issue{}, s.err
	}
	for _, i := range s.issues {
		if i.Key == key {
			return i, nil
		}
	}
	return Issue{}, nil
}
func (s *stubTracker) Create(title string) (Issue, error) {
	if s.err != nil {
		return Issue{}, s.err
	}
	s.created = append(s.created, title)
	return Issue{Key: s.team + "-1", Title: title}, nil
}
func (s *stubTracker) Claim(key string) (Claimed, error) {
	if s.err != nil {
		return Claimed{}, s.err
	}
	s.claims = append(s.claims, key)
	return s.claim, nil
}

func issueAt(key, iso string) Issue {
	t, _ := time.Parse(time.RFC3339, iso)
	return Issue{ID: "uuid-" + key, Key: key, Title: key, UpdatedAt: t}
}

func projectAt(id, name, iso string) Project {
	t, _ := time.Parse(time.RFC3339, iso)
	return Project{ID: id, Name: name, UpdatedAt: t}
}

// twoWorkspaces is a set over a personal workspace with two teams and
// a company one with a third, the shape this was built for.
func twoWorkspaces(a, b *stubTracker, notify func(string)) trackerSet {
	return trackerSet{
		notify: notify,
		workspaces: []workspaceTracker{
			{name: "stefanahman", teams: []string{"DEV", "LIFE"}, tracker: a},
			{name: "norrbrunn", teams: []string{"NOR"}, tracker: b},
		},
	}
}

// TestSetMergesNewestFirst is the promise every list makes and the one
// concatenating two sorted answers breaks.
func TestSetMergesNewestFirst(t *testing.T) {
	a := &stubTracker{issues: []Issue{
		issueAt("DEV-12", "2026-09-10T10:00:00Z"),
		issueAt("LIFE-3", "2026-09-01T10:00:00Z"),
	}}
	b := &stubTracker{issues: []Issue{
		issueAt("NOR-7", "2026-09-14T10:00:00Z"),
		issueAt("NOR-2", "2026-09-05T10:00:00Z"),
	}}
	got, err := twoWorkspaces(a, b, nil).Issues()
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, i := range got {
		keys = append(keys, i.Key)
	}
	want := []string{"NOR-7", "DEV-12", "NOR-2", "LIFE-3"}
	if !reflect.DeepEqual(keys, want) {
		t.Errorf("merged order = %v, want %v", keys, want)
	}
	// Every row knows where it came from; a project row is told by this
	// or not at all.
	for _, i := range got {
		if i.Workspace == "" {
			t.Errorf("%s carries no workspace", i.Key)
		}
	}
	if got[0].Workspace != "norrbrunn" || got[1].Workspace != "stefanahman" {
		t.Errorf("workspaces = %q, %q", got[0].Workspace, got[1].Workspace)
	}
}

// TestSetShowsWhatAnswered is the partial answer: one workspace's key
// has expired, and the other's issues are still worth seeing.
func TestSetShowsWhatAnswered(t *testing.T) {
	a := &stubTracker{issues: []Issue{issueAt("DEV-12", "2026-09-10T10:00:00Z")}}
	b := &stubTracker{err: errors.New("Linear refused the API key")}
	var said []string
	got, err := twoWorkspaces(a, b, func(s string) { said = append(said, s) }).Issues()
	if err != nil {
		t.Fatalf("one workspace failing failed the list: %v", err)
	}
	if len(got) != 1 || got[0].Key != "DEV-12" {
		t.Errorf("issues = %+v, want the one workspace that answered", got)
	}
	if len(said) != 1 || !strings.Contains(said[0], "norrbrunn") {
		t.Errorf("notify said %v, want the workspace that failed named", said)
	}
}

// TestSetFailsWhenAllFail: a list with nothing in it and no error
// would read as "no issues", which is a different thing entirely.
func TestSetFailsWhenAllFail(t *testing.T) {
	a := &stubTracker{err: errors.New("boom a")}
	b := &stubTracker{err: errors.New("boom b")}
	_, err := twoWorkspaces(a, b, nil).Issues()
	if err == nil {
		t.Fatal("every workspace failed and the list did not")
	}
	for _, want := range []string{"stefanahman", "norrbrunn"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

// TestSetRoutesByTeam: DEV-12 is asked of the workspace that claims
// DEV, and nothing is asked of the other.
func TestSetRoutesByTeam(t *testing.T) {
	a := &stubTracker{issues: []Issue{issueAt("LIFE-3", "2026-09-01T10:00:00Z")}}
	b := &stubTracker{issues: []Issue{issueAt("NOR-7", "2026-09-14T10:00:00Z")}}
	set := twoWorkspaces(a, b, nil)

	got, err := set.Issue("LIFE-3")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != "LIFE-3" || got.Workspace != "stefanahman" {
		t.Errorf("LIFE-3 came back as %+v", got)
	}
	got, err = set.Issue("NOR-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Workspace != "norrbrunn" {
		t.Errorf("NOR-7 came from %q", got.Workspace)
	}
	// A claim writes, so it goes to the one workspace and no other.
	if _, err := set.Claim("NOR-7"); err != nil || len(a.claims) != 0 || !reflect.DeepEqual(b.claims, []string{"NOR-7"}) {
		t.Errorf("Claim(NOR-7) reached %q and %q, %v", a.claims, b.claims, err)
	}
	// A team no workspace claims is asked of each rather than refused:
	// the key can be real and `teams` simply incomplete.
	if _, ok := set.forKey("ZZZ-1"); ok {
		t.Error("an unclaimed team routed somewhere")
	}
}

// TestSetFindsAProjectWhereverItIs: a project is a UUID or a slug and
// says nothing about which workspace it lives in, so each is asked in
// turn — and an empty answer is not the answer.
func TestSetFindsAProjectWhereverItIs(t *testing.T) {
	a := &stubTracker{projects: []Project{projectAt("p-a", "Saga", "2026-09-10T10:00:00Z")}}
	b := &stubTracker{projects: []Project{projectAt("p-b", "The Vake", "2026-09-14T10:00:00Z")}}
	set := twoWorkspaces(a, b, nil)

	got, err := set.Project("p-b")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "The Vake" || got.Workspace != "norrbrunn" {
		t.Errorf("project = %+v, want The Vake from norrbrunn", got)
	}
	// One that exists nowhere is empty and not an error, as one
	// workspace answers today.
	if got, err := set.Project("nope"); err != nil || got.ID != "" {
		t.Errorf("a missing project = %+v, %v", got, err)
	}
}

// TestSetMergesProjectsNewestFirst covers the other merged list, whose
// rows carry no key to say where they came from.
func TestSetMergesProjectsNewestFirst(t *testing.T) {
	a := &stubTracker{projects: []Project{projectAt("p-a", "Saga", "2026-09-10T10:00:00Z")}}
	b := &stubTracker{projects: []Project{projectAt("p-b", "The Vake", "2026-09-14T10:00:00Z")}}
	got, err := twoWorkspaces(a, b, nil).Projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "The Vake" || got[0].Workspace != "norrbrunn" {
		t.Fatalf("projects = %+v", got)
	}
	if got[1].Workspace != "stefanahman" {
		t.Errorf("second project came from %q", got[1].Workspace)
	}
}

// TestProjectColsShedWorkspaceLate: the column exists only where there
// is more than one workspace, and when the window narrows it goes
// second to last — before the priority and after everything else.
func TestProjectColsShedWorkspaceLate(t *testing.T) {
	one := model{cfg: Config{Linear: LinearWorkspaces{{}}}, width: 200}
	if got := one.projectCols().workspace; got != 0 {
		t.Errorf("one workspace gave the column %d cells, want none", got)
	}

	two := Config{Linear: LinearWorkspaces{{Name: "a"}, {Name: "b"}}}
	if got := (model{cfg: two, width: 200}).projectCols().workspace; got != workspaceWidth {
		t.Errorf("two workspaces gave the column %d cells, want %d", got, workspaceWidth)
	}

	// Narrowing sheds lead, then initiative, then due, then the
	// workspace — and the priority outlasts them all.
	var order []string
	last := (model{cfg: two, width: 200}).projectCols()
	for w := 200; w >= 30; w-- {
		c := (model{cfg: two, width: w}).projectCols()
		for _, f := range []struct {
			name       string
			was, isNow int
		}{
			{"lead", last.lead, c.lead},
			{"initiative", last.initiative, c.initiative},
			{"due", last.due, c.due},
			{"workspace", last.workspace, c.workspace},
			{"priority", last.priority, c.priority},
		} {
			if f.was > 0 && f.isNow == 0 {
				order = append(order, f.name)
			}
		}
		last = c
	}
	want := []string{"lead", "initiative", "due", "workspace", "priority"}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("columns shed in order %v, want %v", order, want)
	}
}

// TestTitleNamesTheWorkspaces: the issue and project lists are not
// scoped by a repo, so with several workspaces the title says which
// ones answered rather than naming one repo the rows did not all come
// from. `owl · projects · norrbrunn/norrbrunn` over a list holding
// stefanahman's projects too is the bug this pins.
func TestTitleNamesTheWorkspaces(t *testing.T) {
	two := Config{Linear: LinearWorkspaces{{Name: "stefanahman"}, {Name: "norrbrunn"}}}
	m := model{cfg: two, kind: "project", width: 120}
	if got := m.workspaceNames(); got != "stefanahman + norrbrunn" {
		t.Errorf("workspaceNames() = %q", got)
	}
	title := m.titleLine("norrbrunn/norrbrunn")
	if strings.Contains(title, "norrbrunn/norrbrunn") {
		t.Errorf("the title still names one repo for a list from two workspaces: %q", title)
	}
	for _, want := range []string{"stefanahman", "norrbrunn"} {
		if !strings.Contains(title, want) {
			t.Errorf("the title does not name %s: %q", want, title)
		}
	}

	// One workspace keeps the repo: it is the context you are standing
	// in, and a lone workspace is often not even named.
	one := model{cfg: Config{Linear: LinearWorkspaces{{}}}, kind: "project", width: 120}
	if got := one.workspaceNames(); got != "" {
		t.Errorf("one workspace named itself: %q", got)
	}
	if got := one.titleLine("acme/app"); !strings.Contains(got, "acme/app") {
		t.Errorf("one workspace lost the repo from its title: %q", got)
	}

	// And the PR list is repo-scoped whatever the tracker holds.
	pr := model{cfg: two, kind: "pr", width: 120}
	if got := pr.titleLine("acme/app"); !strings.Contains(got, "acme/app") {
		t.Errorf("the PR list lost its repo: %q", got)
	}
}

// TestProjectLegendHasNoGap: the workspace line is appended, not left
// empty, because an empty string is still a line to JoinVertical — a
// blank entry would open a gap in every single-workspace legend.
func TestProjectLegendHasNoGap(t *testing.T) {
	one := model{cfg: Config{Linear: LinearWorkspaces{{}}}}
	if strings.Contains(one.projectLegend(), "\n\n\n") {
		t.Errorf("one workspace: the legend has a double gap:\n%s", one.projectLegend())
	}
	two := model{cfg: Config{Linear: LinearWorkspaces{{Name: "a"}, {Name: "b"}}}}
	if !strings.Contains(two.projectLegend(), "the Linear workspace the project is in") {
		t.Error("two workspaces: the legend does not explain the column")
	}
	if strings.Contains(two.projectLegend(), "\n\n\n") {
		t.Errorf("two workspaces: the legend has a double gap:\n%s", two.projectLegend())
	}
}

// twoWorkspaceProjects is the fixture's projects split across the two
// workspaces of the personal config: the first two stefanahman's, the
// last two norrbrunn's.
func twoWorkspaceProjects() []Project {
	ps := fixtureProjects()
	for i := range ps {
		ps[i].Workspace = "stefanahman"
		if i >= 2 {
			ps[i].Workspace = "norrbrunn"
		}
	}
	return ps
}

func tab(t *testing.T, m model) model {
	t.Helper()
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	return next.(model)
}

// shownProjects is the names of the project rows showing, in order.
func shownProjects(m model) []string {
	var names []string
	for _, row := range m.visibleProjectRows() {
		if row.project != nil {
			names = append(names, row.project.Name)
		}
	}
	return names
}

// TestTabStepsThroughWorkspaces: on the project list Tab narrows to
// each workspace in the config's order and then back to all of them.
// A narrowed list names its workspace in the title, drops the column
// every row would answer alike, and counts only what it shows.
func TestTabStepsThroughWorkspaces(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Linear = LinearWorkspaces{{Name: "stefanahman"}, {Name: "norrbrunn"}}
	m := newProjectModel(cfg, "acme/app", nil, nil)
	m.projects, m.ready, m.width, m.height = twoWorkspaceProjects(), true, 200, 30
	m.resizeViewport()
	if got := len(shownProjects(m)); got != 4 {
		t.Fatalf("before Tab: %d projects, want all 4", got)
	}

	m = tab(t, m)
	if got := shownProjects(m); !reflect.DeepEqual(got, []string{"Sequential Capture redesign", "Bardo Backstage (BACKEND)"}) {
		t.Errorf("first Tab shows %v, want stefanahman's two", got)
	}
	title := stripANSI(m.titleLine("acme/app"))
	if !strings.Contains(title, "stefanahman") || strings.Contains(title, "norrbrunn") {
		t.Errorf("narrowed title: %q, want stefanahman alone", title)
	}
	if got := m.projectCols().workspace; got != 0 {
		t.Errorf("narrowed list kept a workspace column of %d cells", got)
	}
	if got := stripANSI(m.projectCountsSummary()); !strings.HasPrefix(got, "2 projects") {
		t.Errorf("narrowed counts: %q, want 2 projects", got)
	}
	if !m.keys.Pane.Enabled() || m.keys.Pane.Help().Desc != "next Linear workspace" {
		t.Errorf("Tab's help reads %q, enabled %v", m.keys.Pane.Help().Desc, m.keys.Pane.Enabled())
	}

	m = tab(t, m)
	if got := shownProjects(m); !reflect.DeepEqual(got, []string{"Endpoint Validation w. LLM readable errors", "Sven v2 — Deterministic harness"}) {
		t.Errorf("second Tab shows %v, want norrbrunn's two", got)
	}

	m = tab(t, m)
	if got := len(shownProjects(m)); got != 4 || m.projectCols().workspace == 0 {
		t.Errorf("third Tab: %d projects, column %d cells; want all 4 and the column back", got, m.projectCols().workspace)
	}
}

// TestTabNarrowsTheIssueList: the same key on the issue list, which
// merges the same workspaces.
func TestTabNarrowsTheIssueList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Linear = LinearWorkspaces{{Name: "stefanahman", Teams: []string{"DEV"}}, {Name: "norrbrunn", Teams: []string{"NOR"}}}
	mine := mkIssue("DEV-12", "yours", "dev-12-x", "In Progress", "started", 2, time.Hour)
	mine.Workspace = "stefanahman"
	theirs := mkIssue("NOR-7", "the company's", "nor-7-x", "In Progress", "started", 2, 2*time.Hour)
	theirs.Workspace = "norrbrunn"
	m := newIssueModel(cfg, "acme/app", nil, nil)
	m.issues, m.ready, m.width, m.height = []Issue{mine, theirs}, true, 160, 30
	m.resizeViewport()

	keys := func(m model) []string {
		var out []string
		for _, row := range m.visibleIssueRows() {
			if row.issue != nil {
				out = append(out, row.issue.Key)
			}
		}
		return out
	}
	m = tab(t, tab(t, m))
	if got := keys(m); !reflect.DeepEqual(got, []string{"NOR-7"}) {
		t.Errorf("two Tabs show %v, want norrbrunn's NOR-7", got)
	}
	if got := stripANSI(m.issueCountsSummary()); !strings.HasPrefix(got, "1 open") {
		t.Errorf("narrowed counts: %q, want 1 open", got)
	}
}

// TestTabWithOneWorkspace: nothing to step through, so the key is off
// and out of the help, and the list stays whole.
func TestTabWithOneWorkspace(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := newProjectModel(defaultConfig(), "acme/app", nil, nil)
	m.projects, m.ready = fixtureProjects(), true
	if m.keys.Pane.Enabled() {
		t.Error("one workspace: Tab is still in the help")
	}
	if m = tab(t, m); m.onlyWorkspace != "" || len(shownProjects(m)) != 4 {
		t.Errorf("one workspace: Tab narrowed the list to %q", m.onlyWorkspace)
	}
}

// TestTabRemembersTheWorkspace: the next start opens on the workspace
// Tab left, with the cursor on the row it was on there; a workspace the
// config no longer has opens on all of them rather than on nothing.
func TestTabRemembersTheWorkspace(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Linear = LinearWorkspaces{{Name: "stefanahman"}, {Name: "norrbrunn"}}
	m := newProjectModel(cfg, "acme/app", nil, nil)
	m.projects, m.ready = twoWorkspaceProjects(), true
	m = tab(t, tab(t, m)) // norrbrunn
	m.moveCursor(1)
	m.persistCache()

	resumed := newProjectModel(cfg, "acme/app", nil, loadProjectCache())
	if resumed.onlyWorkspace != "norrbrunn" {
		t.Fatalf("resumed on %q, want norrbrunn", resumed.onlyWorkspace)
	}
	if p := resumed.selectedProject(); p == nil || p.Name != "Sven v2 — Deterministic harness" {
		t.Errorf("resumed cursor on %+v, want norrbrunn's second project", p)
	}

	cfg.Linear = LinearWorkspaces{{Name: "stefanahman"}, {Name: "bardo"}}
	if gone := newProjectModel(cfg, "acme/app", nil, loadProjectCache()); gone.onlyWorkspace != "" {
		t.Errorf("a workspace no longer configured narrowed the list to %q", gone.onlyWorkspace)
	}
}

// TestNarrowedEmptyListSaysWhere: a workspace with nothing in it must
// not read as an empty desk, so the empty line names the workspace and
// the key to the next one.
func TestNarrowedEmptyListSaysWhere(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Linear = LinearWorkspaces{{Name: "stefanahman"}, {Name: "norrbrunn"}}
	m := newProjectModel(cfg, "acme/app", nil, nil)
	m.projects, m.ready, m.width, m.height = twoWorkspaceProjects()[:2], true, 120, 30
	m.resizeViewport()
	m = tab(t, tab(t, m)) // norrbrunn, which holds none of these
	if got := stripANSI(m.View().Content); !strings.Contains(got, "no open projects of yours in norrbrunn — tab for the next workspace.") {
		t.Errorf("empty narrowed list reads:\n%s", got)
	}
}

// TestTabInADrillDoesNothing: a drilled project is one workspace
// already, and narrowing behind it would surprise on the way back.
func TestTabInADrillDoesNothing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := defaultConfig()
	cfg.Linear = LinearWorkspaces{{Name: "stefanahman"}, {Name: "norrbrunn"}}
	m := newProjectModel(cfg, "acme/app", nil, nil)
	m.projects, m.ready = twoWorkspaceProjects(), true
	m.drill = &m.projects[0]
	if m = tab(t, m); m.onlyWorkspace != "" {
		t.Errorf("Tab in a drill narrowed the list to %q", m.onlyWorkspace)
	}
}
