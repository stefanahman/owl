package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// ansiRe strips the styles so a test asserts on the text, not on the
// escape codes lipgloss wrapped it in.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func fixtureProjects() []Project {
	mk := func(name, slug, state, stype string, progress float64, scope, milestones int, age time.Duration) Project {
		p := Project{ID: "uuid-" + slug, Name: name, SlugID: slug, URL: "https://linear.app/x/project/" + slug,
			Progress: progress, Scope: scope, UpdatedAt: time.Now().Add(-age)}
		p.State.Name, p.State.Type = state, stype
		p.Lead.Name = "Stefan Åhman"
		for i := 0; i < milestones; i++ {
			p.Milestones.Nodes = append(p.Milestones.Nodes, Milestone{ID: "ms", Name: "M", Progress: 0.5})
		}
		return p
	}
	return []Project{
		mk("Sequential Capture redesign", "a76d38ca8527", "In Progress", "started", 0.62, 127, 11, 2*time.Hour),
		// Named Paused, typed started: it belongs to the In progress
		// section, and the row has to say so.
		mk("Bardo Backstage (BACKEND)", "c67d415965ae", "Paused", "started", 0.08, 917, 0, 8*time.Hour),
		mk("Endpoint Validation w. LLM readable errors", "eb528db32d42", "Planned", "planned", 0.5, 4, 0, 3*24*time.Hour),
		mk("Sven v2 — Deterministic harness", "1bd92aa64c33", "Backlog", "backlog", 0.28, 18, 6, 5*24*time.Hour),
	}
}

// projectIssues are the user's issues across two of the fixture's
// projects, for the yours-over-total column.
func projectIssues() []Issue {
	mine := func(key, project string) Issue {
		is := mkIssue(key, "a title", "bar-x", "In Review", "started", 2, time.Hour)
		is.Project.ID, is.Project.Name = "uuid-"+project, project
		return is
	}
	return []Issue{
		mine("BAR-4079", "a76d38ca8527"),
		mine("BAR-4091", "a76d38ca8527"),
		mine("BAR-4292", "eb528db32d42"),
	}
}

func TestProjectListRendersSectionsAndColumns(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := newProjectModel(defaultConfig(), "acme/example", nil, nil)
	m.projects, m.issues, m.ready = fixtureProjects(), projectIssues(), true
	m.width, m.height = 140, 30
	m.resizeViewport()

	var b strings.Builder
	for _, row := range m.visibleProjectRows() {
		b.WriteString(m.renderRow(row, false) + "\n")
	}
	out := stripANSI(b.String())

	for _, want := range []string{
		"In progress", "Planned", "Backlog",
		"▓▓▓▓▓▓░░░░  62%", // Linear's own fraction, ten cells
		"2/127",           // two of the user's issues, of everything the project holds
		"11 ms",
		"0/917", // scope, not a capped issues connection
		"1/4",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project list lacks %q:\n%s", want, out)
		}
	}
	// A status named Paused but typed started sits under In progress,
	// and the row says Paused because the section does not.
	if !regexp.MustCompile(`Bardo Backstage \(BACKEND\).*Paused`).MatchString(out) {
		t.Errorf("the paused project does not name its state:\n%s", out)
	}
	// A row whose state matches its section does not repeat it.
	if strings.Count(out, "In Progress") != 0 || strings.Count(out, "Backlog") != 1 {
		t.Errorf("a row repeats its section's state name:\n%s", out)
	}
	if got := stripANSI(m.projectCountsSummary()); !strings.Contains(got, "4 projects · 2 in progress · 1 planned · 1 backlog · 3 issues yours") {
		t.Errorf("counts = %q", got)
	}
}

func TestProjectListFiltersByName(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := newProjectModel(defaultConfig(), "acme/example", nil, nil)
	m.projects, m.issues, m.ready = fixtureProjects(), projectIssues(), true
	m.width, m.height = 140, 30
	m.resizeViewport()

	m.search.SetValue("sequential")
	rows := m.visibleProjectRows()
	if len(rows) != 2 || rows[1].project == nil || rows[1].project.Name != "Sequential Capture redesign" {
		t.Errorf("filter by name: %+v", rows)
	}
	m.search.SetValue("nothing here")
	if rows := m.visibleProjectRows(); len(rows) != 0 {
		t.Errorf("filter matching nothing: %+v", rows)
	}
}

func TestProjectCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if loadProjectCache() != nil {
		t.Fatal("a cache from nowhere")
	}
	m := newProjectModel(defaultConfig(), "acme/example", nil, nil)
	m.projects, m.issues, m.ready, m.cursor = fixtureProjects(), projectIssues(), true, 2
	m.lastFetched = time.Now()
	m.persistCache()
	resumed := newProjectModel(defaultConfig(), "acme/example", nil, loadProjectCache())
	if len(resumed.projects) != 4 || len(resumed.issues) != 3 || !resumed.ready || resumed.cursor != 2 {
		t.Errorf("resumed = %d projects, %d issues, ready %v, cursor %d", len(resumed.projects), len(resumed.issues), resumed.ready, resumed.cursor)
	}
	// The issues ride along so the yours-over-total column survives a
	// cold start.
	if resumed.mineIn(resumed.projects[0]) != 2 {
		t.Errorf("mine in the first project = %d, want 2", resumed.mineIn(resumed.projects[0]))
	}
}

func TestProjectTable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	_, srv := newFakeLinear(t, "lin_key")
	orig := linearEndpoint
	linearEndpoint = srv.URL
	t.Cleanup(func() { linearEndpoint = orig })
	cfg := defaultConfig()
	cfg.Linear = LinearConfig{Token: "lin_key", Team: "BAR"}

	var out strings.Builder
	if err := runProject(cfg, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^PROGRESS\s+ISSUES\s+MS\s+STATE\s+PROJECT$`).MatchString(out.String()) {
		t.Errorf("table header:\n%s", out.String())
	}
	if !regexp.MustCompile(`(?m)^62%\s+127\s+2\s+In Progress\s+Sequential Capture redesign$`).MatchString(out.String()) {
		t.Errorf("table rows:\n%s", out.String())
	}
	if err := runProject(cfg, []string{"bogus"}, &out); err == nil {
		t.Error("unknown project command should fail")
	}
}
