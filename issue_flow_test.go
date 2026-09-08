package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stefanahman/mux"
)

// issueFixture is the review fixture with a Linear behind it: two
// issues, one whose branch the origin already has.
func issueFixture(t *testing.T) (*fixture, *fakeLinear) {
	t.Helper()
	f := newFixture(t)
	fake, srv := newFakeLinear(t, "lin_key")
	orig := linearEndpoint
	linearEndpoint = srv.URL
	t.Cleanup(func() { linearEndpoint = orig })
	f.cfg.Linear = LinearConfig{Token: "lin_key", Team: "BAR"}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// BAR-4159's branch exists on the origin, one commit past main.
	f.git(f.origin, "checkout", "-q", "-b", "bar-4159-company-fuzzy-match")
	f.write(filepath.Join(f.origin, "fuzzy.txt"), "started upstream\n")
	f.git(f.origin, "add", ".")
	f.git(f.origin, "commit", "-q", "-m", "BAR-4159 wip")
	f.git(f.origin, "checkout", "-q", "main")
	f.git(f.repo, "fetch", "-q", "origin")
	return f, fake
}

func (f *fixture) openIssue(args ...string) string {
	f.t.Helper()
	var out strings.Builder
	if err := runIssue(f.cfg, append([]string{"open"}, args...), &out); err != nil {
		f.t.Fatalf("issue open %v: %v", args, err)
	}
	return out.String()
}

// featureWindows lists the features session's windows, keepalive included.
func (f *fixture) featureWindows() []string {
	out, _ := tmux("list-windows", "-t", mux.TmuxTarget(f.cfg.Issue.Session, ""), "-F", "#{window_name}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestIssueOpenCreatesTheFeature(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": `echo "$OWL_ISSUE|$OWL_BRANCH|$OWL_SESSION|$OWL_WINDOW|$OWL_WORKTREE|$OWL_REPO" > ` + hookOut}

	// A branch the origin does not have yet: created from its default
	// branch.
	out := f.openIssue("bar-4160")
	name := "bar-4160-per-tenant-override"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if !strings.Contains(out, "creating "+name+" from origin/main into "+wt) || !strings.Contains(out, "started features:"+name) {
		t.Errorf("output: %q", out)
	}
	if got := f.git(wt, "branch", "--show-current"); got != name {
		t.Errorf("worktree branch %q, want %q", got, name)
	}
	if head, want := f.git(wt, "rev-parse", "HEAD"), f.git(f.repo, "rev-parse", "origin/main"); head != want {
		t.Errorf("worktree HEAD %s, want origin/main %s", head, want)
	}
	if got, want := f.featureWindows(), []string{"scratch", name}; !reflect.DeepEqual(got, want) {
		t.Errorf("features windows %v, want %v", got, want)
	}
	if _, err := tmux("has-session", "-t", mux.TmuxTarget("reviews", "")); err == nil {
		t.Error("a feature created the reviews session")
	}
	f.waitPaneIn("features", name, "true '/owl:feature BAR-4160'")
	if cwd, _ := tmux("display-message", "-p", "-t", mux.TmuxTarget("features", name), "#{pane_current_path}"); cwd != wt {
		t.Errorf("window cwd %q, want %q", cwd, wt)
	}
	if link, err := os.Readlink(filepath.Join(wt, ".claude", "settings.local.json")); err != nil || link != filepath.Join(f.repo, ".claude", "settings.local.json") {
		t.Errorf("link_local not applied: %q, %v", link, err)
	}
	f.waitFile(hookOut, "BAR-4160|"+name+"|features|"+name+"|"+wt+"|"+f.repo+"\n")

	// A branch the origin has: tracked, at the origin's commit.
	out = f.openIssue("BAR-4159")
	name2 := "bar-4159-company-fuzzy-match"
	wt2 := filepath.Join(f.repo, ".worktrees.local", name2)
	if !strings.Contains(out, "fetching origin/"+name2+" into "+wt2) {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt2, "fuzzy.txt")) {
		t.Errorf("worktree %s lacks the upstream commit", wt2)
	}
	if up := f.git(wt2, "rev-parse", "--abbrev-ref", "@{upstream}"); up != "origin/"+name2 {
		t.Errorf("upstream %q, want origin/%s", up, name2)
	}

	// Again: the worktree and window exist, so only the selection happens.
	if out := f.openIssue("BAR-4160"); !strings.Contains(out, "selected features:"+name) {
		t.Errorf("second open: %q", out)
	}
	if got := f.featureWindows(); len(got) != 3 {
		t.Errorf("second open added a window: %v", got)
	}

	// The overlay, by issue.
	if ls := findLocalBy(model{cfg: f.cfg}.fetchLocalIn(features).(localMsg), func(n string) bool { return matchesIssue(n, "BAR-4160") }); ls.Worktree != wt || ls.Window != name {
		t.Errorf("overlay = %+v", ls)
	}

	// A start stays put: the window is not selected.
	if _, err := tmux("select-window", "-t", mux.TmuxTarget("features", "scratch")); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runIssue(f.cfg, []string{"start", "BAR-4159"}, &buf); err != nil || !strings.Contains(buf.String(), "ready features:"+name2) {
		t.Errorf("start: %q, %v", buf.String(), err)
	}
	if active := f.activeWindowIn("features"); active != "scratch" {
		t.Errorf("start selected %q", active)
	}
}

func TestIssueClose(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	f.openIssue("BAR-4160")
	name := "bar-4160-per-tenant-override"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	f.waitPaneIn("features", name, "true")

	// A commit nowhere else stops close; so does an edit; --force takes both.
	f.write(filepath.Join(wt, "work.txt"), "a feature\n")
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", "BAR-4160 work")
	if err := runIssue(f.cfg, []string{"close", "BAR-4160"}, io.Discard); err == nil || !strings.Contains(err.Error(), "pushed nowhere") {
		t.Fatalf("close with an unpushed commit: %v, want a refusal", err)
	}
	f.write(filepath.Join(wt, "work.txt"), "edited\n")
	var out strings.Builder
	if err := runIssue(f.cfg, []string{"close", "--force", "BAR-4160"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"removed worktree " + wt, "deleted branch " + name, "closed window features:" + name, "BAR-4160 closed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if f.exists(wt) || slices.Contains(f.branches(), name) {
		t.Error("worktree or branch still there")
	}
	if got, want := f.featureWindows(), []string{"scratch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows %v, want %v", got, want)
	}
	err := runIssue(f.cfg, []string{"close", "BAR-4160"}, &out)
	var nothing nothingToCloseError
	if !errors.As(err, &nothing) {
		t.Errorf("second close: %v, want nothingToCloseError", err)
	}

	// A tracked branch with nothing unpushed closes without --force,
	// and close infers the issue from inside its worktree.
	f.openIssue("BAR-4159")
	name2 := "bar-4159-company-fuzzy-match"
	f.waitPaneIn("features", name2, "true")
	t.Chdir(filepath.Join(f.repo, ".worktrees.local", name2))
	out.Reset()
	if err := runIssue(f.cfg, []string{"close"}, &out); err != nil || !strings.Contains(out.String(), "BAR-4159 closed") {
		t.Errorf("close from inside: %q, %v", out.String(), err)
	}
	// The origin's branch is untouched.
	if _, err := git(f.origin, "rev-parse", "--verify", "refs/heads/"+name2); err != nil {
		t.Errorf("close deleted the remote branch: %v", err)
	}
}

func TestIssueKeyOf(t *testing.T) {
	cases := map[string]string{
		"bar-4159-company-fuzzy-match": "BAR-4159",
		"BAR-4159":                     "BAR-4159",
		"eng-1":                        "ENG-1",
		"pr-42-fix":                    "", // a review
		"pr-42":                        "",
		"scratch":                      "",
		"feature/x":                    "",
		"bar4159":                      "",
	}
	for name, want := range cases {
		if got := issueKeyOf(name); got != want {
			t.Errorf("issueKeyOf(%q) = %q, want %q", name, got, want)
		}
	}
	if !matchesIssue("bar-4159-x", "bar-4159") || matchesIssue("bar-41590-x", "BAR-4159") {
		t.Error("matchesIssue")
	}
	if k, err := parseIssueKey("bar-7"); err != nil || k != "BAR-7" {
		t.Errorf("parseIssueKey = %q, %v", k, err)
	}
	for _, bad := range []string{"", "42", "bar", "bar-", "-7", "bar-7-x"} {
		if _, err := parseIssueKey(bad); err == nil {
			t.Errorf("parseIssueKey(%q): expected an error", bad)
		}
	}
	if !isWorkspaceName("pr-42") || !isWorkspaceName("bar-4159-x") || isWorkspaceName("main") {
		t.Error("isWorkspaceName")
	}
}
