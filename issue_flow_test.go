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
	f.cfg.Linear = LinearWorkspaces{{Token: "lin_key", Team: "ENG"}}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// ENG-4159's branch exists on the origin, one commit past main.
	f.git(f.origin, "checkout", "-q", "-b", "eng-4159-address-fuzzy-match")
	f.write(filepath.Join(f.origin, "fuzzy.txt"), "started upstream\n")
	f.git(f.origin, "add", ".")
	f.git(f.origin, "commit", "-q", "-m", "ENG-4159 wip")
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

// TestIssueOpenOnABase: an issue that is a layer of a stack starts
// from the layer below, not from the trunk, and says so where the next
// session can find it.
//
// The saying is the point. A briefing is one prompt in one
// conversation; the session that picks this branch up tomorrow never
// read it, and that session is exactly the one that must not rebase
// onto main or force-push over the layer below.
func TestIssueOpenOnABase(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)

	// The layer below: a branch on the origin with a commit main lacks.
	f.write(filepath.Join(f.origin, "below.txt"), "the layer below\n")
	f.git(f.origin, "checkout", "-q", "-b", "stack-below")
	f.git(f.origin, "add", ".")
	f.git(f.origin, "commit", "-q", "-m", "the layer below")
	f.git(f.origin, "checkout", "-q", "main")
	f.git(f.repo, "fetch", "-q", "origin")

	out := f.openIssue("eng-4160", "--base", "stack-below")

	name := "eng-4160-per-tenant-override"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if !strings.Contains(out, "creating "+name+" from origin/stack-below") {
		t.Errorf("output: %q", out)
	}
	// It carries the layer below's commit, which main does not have.
	if !f.exists(filepath.Join(wt, "below.txt")) {
		t.Error("the worktree does not hold the layer below's work")
	}
	// And git itself answers what this branch follows.
	if got := baseOf(f.repo, name); got != "stack-below" {
		t.Errorf("baseOf = %q, want the base it was opened on", got)
	}
	// An ordinary feature records nothing, and its absence is the answer.
	f.openIssue("ENG-4159")
	if got := baseOf(f.repo, "eng-4159-address-fuzzy-match"); got != "" {
		t.Errorf("an ordinary feature recorded a base: %q", got)
	}
}

// TestIssueOpenOnABaseAfterTheFact: a layer is often recognised once
// the work has started. The branch point is behind us then, but the
// record is not — it is what the next session reads before it rebases
// — so --base still writes it, and says which half happened.
func TestIssueOpenOnABaseAfterTheFact(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	f.write(filepath.Join(f.origin, "below.txt"), "the layer below\n")
	f.git(f.origin, "checkout", "-q", "-b", "stack-below")
	f.git(f.origin, "add", ".")
	f.git(f.origin, "commit", "-q", "-m", "the layer below")
	f.git(f.origin, "checkout", "-q", "main")

	f.openIssue("eng-4160") // an ordinary open first
	name := "eng-4160-per-tenant-override"
	if got := baseOf(f.repo, name); got != "" {
		t.Fatalf("baseOf = %q before any base was given", got)
	}

	out := f.openIssue("eng-4160", "--base", "stack-below")

	if got := baseOf(f.repo, name); got != "stack-below" {
		t.Errorf("baseOf = %q, want the base given after the fact", got)
	}
	if !strings.Contains(out, "not re-cut") {
		t.Errorf("output does not say the branch was left where it was: %q", out)
	}
}

// TestIssueOpenRejectsAMissingBase: the layer below may simply not be
// pushed yet, and that is worth saying.
func TestIssueOpenRejectsAMissingBase(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	var out strings.Builder
	err := runIssue(f.cfg, []string{"open", "eng-4160", "--base", "no-such-branch"}, &out)
	if err == nil || !strings.Contains(err.Error(), "no such branch on origin") {
		t.Errorf("got %v, want a word about the branch not being there", err)
	}
}

func TestIssueOpenCreatesTheFeature(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": `echo "$OWL_ISSUE|$OWL_BRANCH|$OWL_SESSION|$OWL_WINDOW|$OWL_WORKTREE|$OWL_REPO" > ` + hookOut}

	// A branch the origin does not have yet: created from its default
	// branch.
	out := f.openIssue("eng-4160")
	name := "eng-4160-per-tenant-override"
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
	if got := promptIn(t, f.waitPaneIn("features", name, "$(cat ")); !strings.Contains(got, "/owl:feature ENG-4160") {
		t.Errorf("the agent was handed %q", got)
	}
	if cwd, _ := tmux("display-message", "-p", "-t", mux.TmuxTarget("features", name), "#{pane_current_path}"); cwd != wt {
		t.Errorf("window cwd %q, want %q", cwd, wt)
	}
	if link, err := os.Readlink(filepath.Join(wt, ".claude", "settings.local.json")); err != nil || link != filepath.Join(f.repo, ".claude", "settings.local.json") {
		t.Errorf("link_local not applied: %q, %v", link, err)
	}
	f.waitFile(hookOut, "ENG-4160|"+name+"|features|"+name+"|"+wt+"|"+f.repo+"\n")

	// A branch the origin has: tracked, at the origin's commit.
	out = f.openIssue("ENG-4159")
	name2 := "eng-4159-address-fuzzy-match"
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
	if out := f.openIssue("ENG-4160"); !strings.Contains(out, "selected features:"+name) {
		t.Errorf("second open: %q", out)
	}
	if got := f.featureWindows(); len(got) != 3 {
		t.Errorf("second open added a window: %v", got)
	}

	// The overlay, by issue.
	if ls := findLocalBy(model{cfg: f.cfg}.fetchLocalIn(features).(localMsg), func(n string) bool { return matchesIssue(n, "ENG-4160") }); ls.Worktree != wt || ls.Window != name {
		t.Errorf("overlay = %+v", ls)
	}

	// A start stays put: the window is not selected.
	if _, err := tmux("select-window", "-t", mux.TmuxTarget("features", "scratch")); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if err := runIssue(f.cfg, []string{"start", "ENG-4159"}, &buf); err != nil || !strings.Contains(buf.String(), "ready features:"+name2) {
		t.Errorf("start: %q, %v", buf.String(), err)
	}
	if active := f.activeWindowIn("features"); active != "scratch" {
		t.Errorf("start selected %q", active)
	}
}

// TestIssueOpenClaimsTheIssue: opening an issue makes it the user's in
// Linear, once, and says so; `issue.claim: false` leaves Linear alone.
func TestIssueOpenClaimsTheIssue(t *testing.T) {
	f, fake := issueFixture(t)
	t.Chdir(f.repo)
	reads := func() int {
		n := 0
		for _, q := range fake.queries {
			if q == claimQuery {
				n++
			}
		}
		return n
	}

	if out := f.openIssue("ENG-4160"); !strings.Contains(out, "ENG-4160: Todo → In Progress, assigned to you\n") {
		t.Errorf("output: %q", out)
	}
	if want := []string{"uuid-ENG-4160 state-in-progress me-uuid"}; !slices.Equal(fake.updates, want) {
		t.Errorf("updates = %q, want %q", fake.updates, want)
	}
	// Again: in progress and the user's now, so nothing is written or said.
	if out := f.openIssue("ENG-4160"); strings.Contains(out, "ENG-4160: ") || len(fake.updates) != 1 {
		t.Errorf("second open: %q, %d updates", out, len(fake.updates))
	}

	f.cfg.Issue.Claim = false
	before := reads()
	if out := f.openIssue("ENG-4161"); strings.Contains(out, "ENG-4161: ") {
		t.Errorf("open with claim off: %q", out)
	}
	if reads() != before || len(fake.updates) != 1 {
		t.Errorf("claim off still asked Linear: %d claim reads, %d updates", reads()-before, len(fake.updates))
	}
}

// commitAt commits with a fixed committer date, so "newest branch" is
// decided by the fixture and not by how fast the test ran.
func (f *fixture) commitAt(dir, when, msg string) {
	f.t.Helper()
	if err := os.Setenv("GIT_COMMITTER_DATE", when); err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = os.Unsetenv("GIT_COMMITTER_DATE") }()
	f.git(dir, "commit", "-q", "-m", msg)
}

// pushBranch puts a branch carrying the issue's key on the origin,
// named the way a person or an agent names one — the key, then a slug
// of their own, not the slug Linear names.
func (f *fixture) pushBranch(branch, file, when string) {
	f.t.Helper()
	f.git(f.origin, "checkout", "-q", "-b", branch)
	f.write(filepath.Join(f.origin, file), "the real work\n")
	f.git(f.origin, "add", ".")
	f.commitAt(f.origin, when, branch)
	f.git(f.origin, "checkout", "-q", "main")
}

func TestIssueOpenTakesTheBranchThatCarriesTheKey(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	// ENG-4160's work was pushed from a branch of its own. Linear names
	// eng-4160-per-tenant-override; nobody used it.
	f.pushBranch("eng-4160-cursor-flip", "flip.txt", "2026-01-01T00:00:00Z")

	wt := filepath.Join(f.repo, ".worktrees.local", "eng-4160-cursor-flip")
	if out := f.openIssue("eng-4160"); !strings.Contains(out, "fetching origin/eng-4160-cursor-flip into "+wt) {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "flip.txt")) {
		t.Errorf("%s lacks the work on the branch", wt)
	}
	// The point of the whole thing: no second branch beside the real one.
	if refExists(f.repo, "refs/heads/eng-4160-per-tenant-override") {
		t.Error("open created Linear's branch beside the one carrying the key")
	}
}

func TestIssueOpenTakesTheNewestOfSeveral(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	f.pushBranch("eng-4160-first-try", "first.txt", "2026-01-01T00:00:00Z")
	f.pushBranch("eng-4160-second-try", "second.txt", "2026-06-01T00:00:00Z")

	out := f.openIssue("eng-4160")
	for _, want := range []string{
		"ENG-4160: 2 branches carry the key, taking the newest",
		"▸ eng-4160-second-try",
		"  eng-4160-first-try",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	wt := filepath.Join(f.repo, ".worktrees.local", "eng-4160-second-try")
	if got := f.git(wt, "branch", "--show-current"); got != "eng-4160-second-try" {
		t.Errorf("worktree branch %q", got)
	}
}

func TestIssueOpenReusesAWorktreeItDidNotMake(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	// Another agent's worktree, outside worktrees_dir, on a branch
	// carrying the key. git will not check that branch out twice, so
	// owl has to use it where it stands.
	foreign := filepath.Join(f.root, "elsewhere", "eng-4160-flip")
	f.git(f.repo, "worktree", "add", "-q", "-b", "eng-4160-cursor-flip", foreign, "origin/main")

	out := f.openIssue("eng-4160")
	for _, want := range []string{"eng-4160-cursor-flip is checked out outside .worktrees.local", foreign, "opening it there"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", "eng-4160-flip")) {
		t.Error("open made a second worktree for a branch already checked out")
	}
	// close leaves what it did not create, and says so.
	var buf strings.Builder
	if err := runIssue(f.cfg, []string{"close", "ENG-4160"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "eng-4160-cursor-flip is checked out in "+foreign+", leaving it") {
		t.Errorf("close output: %q", buf.String())
	}
	if !f.exists(foreign) || !refExists(f.repo, "refs/heads/eng-4160-cursor-flip") {
		t.Error("close removed a worktree or branch it does not own")
	}

	// A worktree whose directory is gone is git's "prunable": still
	// listed, never handed out.
	if err := os.RemoveAll(foreign); err != nil {
		t.Fatal(err)
	}
	out = f.openIssue("eng-4160")
	if strings.Contains(out, foreign) {
		t.Errorf("open reused a worktree whose directory is gone:\n%s", out)
	}
}

// A team with a repository of its own opens there, from whichever
// repository the list runs in: owl merges several companies' issues into
// one list, and one's branch must never land in another's repo. The list
// sees the worktree from where it runs, and close finds it there too.
func TestIssueGoesToItsTeamsRepo(t *testing.T) {
	f, _ := issueFixture(t)
	own := filepath.Join(f.root, "eng-repo")
	f.git(f.root, "clone", "-q", f.origin, own)
	f.cfg.Repos = map[string]string{"ENG": own}
	t.Chdir(f.repo)

	f.openIssue("ENG-4160")
	name := "eng-4160-per-tenant-override"
	wt := filepath.Join(own, ".worktrees.local", name)
	if !f.exists(wt) {
		t.Fatalf("no worktree in the team's repository at %s", wt)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", name)) {
		t.Error("the issue was opened in the repository the list runs in")
	}
	if got := readWorktrees(f.cfg)[name].Path; got != wt {
		t.Errorf("the list, run in another repository, has the worktree at %q", got)
	}
	var out strings.Builder
	if err := runIssue(f.cfg, []string{"close", "ENG-4160"}, &out); err != nil {
		t.Fatal(err)
	}
	if f.exists(wt) {
		t.Errorf("close left the worktree in the team's repository:\n%s", out.String())
	}
}

func TestIssueOpenNamesTheRepoThatIsNotOne(t *testing.T) {
	f, _ := issueFixture(t)
	f.cfg.Repos = map[string]string{"ENG": filepath.Join(f.root, "nowhere")}
	t.Chdir(f.repo)
	err := runIssue(f.cfg, []string{"open", "ENG-4160"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "repos.ENG") {
		t.Fatalf("open with a repo that is not one: %v, want an error naming repos.ENG", err)
	}
}

// issue.cmd is what a feature's workspace starts, in place of agent.cmd:
// agent.cmd with --remote-control, say, so the features are reachable
// from claude.ai and the Claude app while the reviews are not.
func TestIssueOpenStartsWithIssueCmd(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	ran := filepath.Join(f.root, "ran")
	f.cfg.Issue.Cmd = "echo issue.cmd >> " + shellQuote(ran) + " #" // the rest of the line is ignored
	f.cfg.Agent.Cmd = "echo agent.cmd >> " + shellQuote(ran) + " #"
	f.openIssue("ENG-4160")
	f.waitFile(ran, "issue.cmd\n")
}

// A feature's conversation is named for its issue, key and title, as one
// argument: under Remote Control that is the name claude.ai and the
// Claude app list it by.
func TestIssueOpenNamesTheSession(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	named := nameAgent(t, f)
	f.openIssue("ENG-4160")
	f.waitFile(named, "ENG-4160 Per-tenant override\n")
}

// Linear out of reach, the conversation is named for the key alone, and
// the open goes on.
func TestIssueOpenNamesTheSessionByKeyWithoutLinear(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	named := nameAgent(t, f)
	f.cfg.Linear[0].Token = "not-the-key"
	f.openIssue("ENG-4159") // its branch is on the remote: nothing else in open needs Linear
	f.waitFile(named, "ENG-4159\n")
}

// The name costs a Linear lookup, so it is asked for only when an agent
// starts: a prompt to the agent already running asks Linear nothing, and
// waits on nothing when Linear is slow.
func TestIssueStartAsksNoNameOfARunningAgent(t *testing.T) {
	f, fake := issueFixture(t)
	t.Chdir(f.repo)
	f.cfg.Issue.Claim = false            // claiming asks Linear on every open, by design
	f.cfg.Agent.Cmd = "cat >/dev/null #" // stays in the foreground; the rest of the line is ignored
	f.openIssue("ENG-4160")
	f.waitPaneIn("features", "eng-4160-per-tenant-override", "cat ")
	before := len(fake.queries)
	if err := runIssue(f.cfg, []string{"start", "ENG-4160", "--prompt", "and the tests"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if asked := fake.queries[before:]; len(asked) > 0 {
		t.Errorf("a prompt to a running agent asked Linear %d times:\n%s", len(asked), strings.Join(asked, "\n"))
	}
}

func TestIssueClose(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	f.openIssue("ENG-4160")
	name := "eng-4160-per-tenant-override"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	f.waitPaneIn("features", name, "true")

	// A commit nowhere else stops close; so does an edit; --force takes both.
	f.write(filepath.Join(wt, "work.txt"), "a feature\n")
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", "ENG-4160 work")
	if err := runIssue(f.cfg, []string{"close", "ENG-4160"}, io.Discard); err == nil || !strings.Contains(err.Error(), "pushed nowhere") {
		t.Fatalf("close with an unpushed commit: %v, want a refusal", err)
	}
	f.write(filepath.Join(wt, "work.txt"), "edited\n")
	var out strings.Builder
	if err := runIssue(f.cfg, []string{"close", "--force", "ENG-4160"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"removed worktree " + wt, "deleted branch " + name, "closed window features:" + name, "ENG-4160 closed"} {
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
	err := runIssue(f.cfg, []string{"close", "ENG-4160"}, &out)
	var nothing nothingToCloseError
	if !errors.As(err, &nothing) {
		t.Errorf("second close: %v, want nothingToCloseError", err)
	}

	// A tracked branch with nothing unpushed closes without --force,
	// and close infers the issue from inside its worktree.
	f.openIssue("ENG-4159")
	name2 := "eng-4159-address-fuzzy-match"
	f.waitPaneIn("features", name2, "true")
	t.Chdir(filepath.Join(f.repo, ".worktrees.local", name2))
	out.Reset()
	if err := runIssue(f.cfg, []string{"close"}, &out); err != nil || !strings.Contains(out.String(), "ENG-4159 closed") {
		t.Errorf("close from inside: %q, %v", out.String(), err)
	}
	// The origin's branch is untouched.
	if _, err := git(f.origin, "rev-parse", "--verify", "refs/heads/"+name2); err != nil {
		t.Errorf("close deleted the remote branch: %v", err)
	}
}

func TestIssueKeyOf(t *testing.T) {
	cases := map[string]string{
		"eng-4159-address-fuzzy-match": "ENG-4159",
		"ENG-4159":                     "ENG-4159",
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
	if !matchesIssue("eng-4159-x", "eng-4159") || matchesIssue("eng-41590-x", "ENG-4159") {
		t.Error("matchesIssue")
	}
	if k, err := parseIssueKey("eng-7"); err != nil || k != "ENG-7" {
		t.Errorf("parseIssueKey = %q, %v", k, err)
	}
	for _, bad := range []string{"", "42", "bar", "bar-", "-7", "eng-7-x"} {
		if _, err := parseIssueKey(bad); err == nil {
			t.Errorf("parseIssueKey(%q): expected an error", bad)
		}
	}
	if !isWorkspaceName("pr-42") || !isWorkspaceName("eng-4159-x") || isWorkspaceName("main") {
		t.Error("isWorkspaceName")
	}
}
