package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestByIssueKey uses the branch names bardo-system actually has open:
// only one of them is the slug Linear names, the rest carry the key
// and a slug of their author's own.
func TestByIssueKey(t *testing.T) {
	index := byIssueKey([]PR{
		{Number: 4006, HeadRefName: "bar-4159-company-fuzzy-match-particle-guard"},
		{Number: 3975, HeadRefName: "bar-4079-shadow-validation-verdane-activity-creation-captured-data"},
		{Number: 4002, HeadRefName: "bar-4091-activity-unit-by-acquisition-method"},
		{Number: 3998, HeadRefName: "bar-4091-count-dimension-normalization"},
		{Number: 4114, HeadRefName: "fix/bar-4157-activity-ef-projection"},
		{Number: 4076, HeadRefName: "codex/bar-4095-freight-quantity"},
		{Number: 4279, HeadRefName: "dependabot/npm_and_yarn/sharp-0.35.4"},
	})
	for _, tc := range []struct {
		key  string
		want []int // PR numbers, newest first
	}{
		{"BAR-4159", []int{4006}},
		{"BAR-4079", []int{3975}},
		{"BAR-4091", []int{4002, 3998}}, // two at once, newest first
		{"BAR-4157", []int{4114}},       // the key past a `fix/` prefix
		{"BAR-4095", []int{4076}},
		{"BAR-4375", nil}, // an issue nobody opened a PR for
	} {
		var got []int
		for _, pr := range index[tc.key] {
			got = append(got, pr.Number)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
				break
			}
		}
	}
	// A dependency bump's version reads as a key; it is filed under one
	// nothing looks up, and never under a real issue's.
	if len(index["SHARP-0"]) != 1 {
		t.Errorf("sharp-0.35.4 indexed as %v", index)
	}
	// The cache round-trips the index: every PR once, however many
	// issues it is filed under.
	if flat := flattenPRs(index); len(flat) != 7 || flat[0].Number != 4279 {
		t.Errorf("flattenPRs = %v", flat)
	}
}

func TestMilestoneSections(t *testing.T) {
	ms := func(key, id, name string, order float64, age time.Duration) Issue {
		is := mkIssue(key, "a title", "b", "Backlog", "backlog", 2, age)
		is.Milestone.ID, is.Milestone.Name, is.Milestone.SortOrder = id, name, order
		return is
	}
	// Deliberately out of order, and with the unmilestoned in the
	// middle: the project's own sortOrder decides, not the input.
	got := milestoneSections([]Issue{
		ms("BAR-5", "m5", "M5 — Output evaluation", 4163, 3*time.Hour),
		ms("BAR-0", "", "", 0, 2*time.Hour),
		ms("BAR-3", "m3", "M3 — Core parity", 900, time.Hour),
		ms("BAR-35", "m35", "M3.5 — Downstream", 2603.5, time.Hour),
		ms("BAR-5b", "m5", "M5 — Output evaluation", 4163, time.Hour),
	})
	var titles []string
	for _, s := range got {
		titles = append(titles, s.title)
	}
	want := []string{"M3 — Core parity", "M3.5 — Downstream", "M5 — Output evaluation", "No milestone"}
	if strings.Join(titles, " | ") != strings.Join(want, " | ") {
		t.Errorf("sections = %v, want %v", titles, want)
	}
	// One section per milestone, newest change first inside it:
	// BAR-5b was touched an hour ago, BAR-5 three hours ago.
	m5 := got[2]
	if len(m5.issues) != 2 || m5.issues[0].Key != "BAR-5b" || m5.issues[1].Key != "BAR-5" {
		t.Errorf("M5 = %+v", m5.issues)
	}
	// Issues with no milestone are not an error and get a heading.
	if last := got[3]; len(last.issues) != 1 || last.issues[0].Key != "BAR-0" {
		t.Errorf("unmilestoned = %+v", last.issues)
	}
}

func TestProjectFlag(t *testing.T) {
	for _, c := range []struct {
		args []string
		id   string
		ok   bool
	}{
		{[]string{"--project", "sequential"}, "sequential", true},
		{[]string{"--project=sequential"}, "sequential", true},
		{[]string{"open", "BAR-1"}, "", false},
		{nil, "", false},
	} {
		id, ok, err := projectFlag(c.args)
		if err != nil || id != c.id || ok != c.ok {
			t.Errorf("projectFlag(%v) = %q, %v, %v", c.args, id, ok, err)
		}
	}
	if _, _, err := projectFlag([]string{"--project"}); err == nil {
		t.Error("--project with no value should fail")
	}
}

func TestIssueListAndHoot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	fake, srv := newFakeLinear(t, "lin_key")
	orig := linearEndpoint
	linearEndpoint = srv.URL
	t.Cleanup(func() { linearEndpoint = orig })
	cfg := defaultConfig()
	cfg.Linear = LinearConfig{Token: "lin_key", Team: "BAR"}
	var out strings.Builder
	if err := runIssue(cfg, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^KEY\s+PRIO\s+AGE\s+STATE\s+PROJECT\s+TITLE$`).MatchString(out.String()) {
		t.Errorf("list header:\n%s", out.String())
	}
	if !regexp.MustCompile(`(?m)^BAR-4159\s+!!\s+\S+\s+In Review\s+Sequential Capture rede…\s+Company fuzzy match$`).MatchString(out.String()) || !strings.Contains(out.String(), "BAR-4160") {
		t.Errorf("list rows:\n%s", out.String())
	}
	out.Reset()
	if err := runIssue(cfg, []string{"new", "Fix", "the", "owl"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "BAR-9001  Fix the owl\nhttps://linear.app/x/issue/BAR-9001") || len(fake.created) != 1 || fake.created[0] != "Fix the owl" {
		t.Errorf("hoot: %q, created %v", out.String(), fake.created)
	}
	if err := runIssue(cfg, []string{"new"}, &out); err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Errorf("hoot without a title: %v", err)
	}
	if err := runIssue(cfg, []string{"bogus"}, &out); err == nil {
		t.Error("unknown issue command should fail")
	}
	// No token: the message says what to configure.
	cfg.Linear.Token = ""
	if err := runIssue(cfg, nil, &out); err == nil || !strings.Contains(err.Error(), "no token configured") {
		t.Errorf("without a token: %v", err)
	}
}
