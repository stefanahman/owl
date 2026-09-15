package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
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
