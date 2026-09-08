package main

import (
	"regexp"
	"strings"
	"testing"
)

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
	if !regexp.MustCompile(`(?m)^KEY\s+PRIO\s+AGE\s+STATE\s+TITLE$`).MatchString(out.String()) {
		t.Errorf("list header:\n%s", out.String())
	}
	if !regexp.MustCompile(`(?m)^BAR-4159\s+!!\s+\S+\s+In Review\s+Company fuzzy match$`).MatchString(out.String()) || !strings.Contains(out.String(), "BAR-4160") {
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
