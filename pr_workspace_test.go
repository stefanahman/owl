package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stefanahman/mux"
)

func TestIssueKeysFor(t *testing.T) {
	for _, c := range []struct {
		name, branch, team string
		want               []string
	}{
		{"linear's own branch", "bar-4157-drop-legacy-tenant-shim", "BAR", []string{"BAR-4157"}},
		{"the key anywhere in it", "fix/bar-4157-projection", "BAR", []string{"BAR-4157"}},
		{"case does not matter", "BAR-4157-thing", "bar", []string{"BAR-4157"}},
		// A branch may close several at once, and the newest is the one
		// being worked on — not the one the branch happens to lead with.
		{"two issues, newest first", "bar-4157-and-bar-4160-both", "BAR", []string{"BAR-4160", "BAR-4157"}},
		// The gate. issueKeysIn is permissive by design, so without the
		// team a dependency bump reads as SHARP-0 and would be filed as a
		// feature of yours.
		{"a version is not an issue", "deps/sharp-0.35.4", "BAR", nil},
		{"another team's issue", "foo-12-something", "BAR", nil},
		{"no key at all", "refactor/tidy-imports", "BAR", nil},
		// Without a team configured there is nothing to check a key
		// against, so nothing is inferred.
		{"no team configured", "bar-4157-thing", "", nil},
	} {
		if got := issueKeysFor(c.branch, c.team); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: issueKeysFor(%q, %q) = %v, want %v", c.name, c.branch, c.team, got, c.want)
		}
	}
	if got := issueKeyFor("bar-4157-and-bar-4160-both", "BAR"); got != "BAR-4160" {
		t.Errorf("issueKeyFor took %q, want the newest BAR-4160", got)
	}
	if got := issueKeyFor("deps/sharp-0.35.4", "BAR"); got != "" {
		t.Errorf("issueKeyFor(%q) = %q, want none", "deps/sharp-0.35.4", got)
	}
}

func TestScopeForName(t *testing.T) {
	for name, want := range map[string]string{
		"pr-42":                     "reviews",
		"pr-42-fix-crash":           "reviews",
		"bar-4157-drop-tenant-shim": "features",
		"proj-sequential-capture":   "projects",
	} {
		if got := scopeForName(name).name; got != want {
			t.Errorf("scopeForName(%q) = %s, want %s", name, got, want)
		}
	}
}

// TestLookupPRTakesBothRoads covers the two ways owl can learn whose
// PR it is. With an owner/name it asks GraphQL, which answers
// viewerDidAuthor and so needs no second call to ask who you are;
// without one there is nothing to query with and it falls back to `pr
// view` plus `gh api user`.
func TestLookupPRTakesBothRoads(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1 $2" in
  "api graphql") printf '%s' '{"data":{"repository":{"pullRequest":{"title":"A Title","headRefName":"bar-4157-x","isCrossRepository":false,"viewerDidAuthor":true}}}}' ;;
  "api user") echo you ;;
  "pr view") printf '%s' '{"title":"A Title","headRefName":"bar-4157-x","isCrossRepository":false,"author":{"login":"you"}}' ;;
  *) echo "unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, slug := range []string{"acme/app", ""} {
		f, ok := lookupPR(dir, slug, 42)
		if !ok {
			t.Fatalf("slug %q: lookup failed", slug)
		}
		if f.Title != "A Title" || f.HeadRefName != "bar-4157-x" || f.CrossRepo {
			t.Errorf("slug %q: facts = %+v", slug, f)
		}
		if !f.Mine {
			t.Errorf("slug %q: the PR is yours and lookupPR says it is not", slug)
		}
	}

	// A PR that is not there answers nothing rather than a zero value
	// that would read as someone else's.
	empty := `#!/bin/sh
case "$1 $2" in
  "api graphql") printf '%s' '{"data":{"repository":{"pullRequest":null}}}' ;;
  *) exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(empty), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := lookupPR(dir, "acme/app", 42); ok {
		t.Error("a missing PR looked up fine")
	}
}

func TestFirstPromptFollowsTheWorkspace(t *testing.T) {
	cfg := defaultConfig()

	theirs := prWorkspace{name: "pr-42-fix", branch: "pr-42-fix"}
	if got := firstPrompt(cfg, 42, theirs); got != "/owl:review 42" {
		t.Errorf("someone else's PR opens on %q, want the review skill", got)
	}

	// Yours never opens on the review skill: that procedure ends in a
	// review posted with gh, and you cannot review yourself.
	yours := prWorkspace{name: "bar-4157-shim", branch: "bar-4157-shim", key: "BAR-4157", own: true}
	got := firstPrompt(cfg, 4290, yours)
	if strings.Contains(got, "/owl:review") {
		t.Errorf("your own PR opens on the review skill: %q", got)
	}
	for _, want := range []string{"#4290", "bar-4157-shim"} {
		if !strings.Contains(got, want) {
			t.Errorf("the prompt does not name %s: %q", want, got)
		}
	}
	// A branch with no issue leaves no placeholder standing.
	if got := firstPrompt(cfg, 9, prWorkspace{name: "pr-9-x", branch: "tidy-imports", own: true}); strings.Contains(got, "{") {
		t.Errorf("an unsubstituted placeholder: %q", got)
	}

	// And none of it fires on a workspace that already holds a
	// conversation: that resumes, and a resumed conversation is sent no
	// prompt at all. The first prompt is for arriving somewhere new.
	if line := startLine(cfg.Agent.Cmd, firstPrompt(cfg, 4290, yours), true, ""); line != cfg.Agent.Cmd+" -c" {
		t.Errorf("resuming sent a prompt: %q", line)
	}
}

// TestReopeningAsksGitHubNothing proves the fast path by taking gh
// away: a workspace owl has already made answers every question about
// itself off the disk, so re-opening one — the commonest thing the
// list does — costs no round trip.
func TestReopeningAsksGitHubNothing(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("42")

	if err := os.WriteFile(filepath.Join(f.root, "bin", "gh"), []byte("#!/bin/sh\necho 'gh: asked' >&2; exit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := f.open("42")

	name := "pr-42-fix-crash-on-startup"
	if !strings.Contains(out, name) {
		t.Errorf("re-open without gh: %q", out)
	}
	if strings.Contains(out, "fetching") {
		t.Errorf("re-open fetched again: %q", out)
	}
}

// ownFixture is the fixture with PR 42 belonging to you, pushed to
// head, and BAR configured as the team.
func ownFixture(t *testing.T, head string) *fixture {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.prIsYours(head)
	f.cfg.Linear.Team = "BAR"
	// The branch exists on the remote, as an open PR's head does.
	f.git(f.origin, "branch", head, "refs/pull/42/head")
	return f
}

func TestOpenYourOwnPRTakesItsBranch(t *testing.T) {
	head := "bar-4157-drop-legacy-tenant-shim"
	f := ownFixture(t, head)

	out := f.open("42")

	// Named for the branch, which is what `owl issue open BAR-4157`
	// would have called it, and so in the features container.
	wt := filepath.Join(f.repo, ".worktrees.local", head)
	if !strings.Contains(out, "started features:"+head) {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "pr42.txt")) {
		t.Fatalf("worktree %s missing the PR's file", wt)
	}
	// The real branch, tracking the remote — not a copy under a name
	// only owl knows, which nothing could push and close would delete.
	if got := f.git(wt, "branch", "--show-current"); got != head {
		t.Errorf("worktree branch %q, want the PR's own %q", got, head)
	}
	if got := f.git(wt, "rev-parse", "--abbrev-ref", "@{upstream}"); got != "origin/"+head {
		t.Errorf("upstream %q, want origin/%s", got, head)
	}
	for _, br := range f.branches() {
		if strings.HasPrefix(br, "pr-42") {
			t.Errorf("a copy of your own branch was made: %s", br)
		}
	}
}

func TestOpenYourOwnPRJoinsTheFeatureAlreadyOpen(t *testing.T) {
	head := "bar-4157-drop-legacy-tenant-shim"
	f := ownFixture(t, head)
	// The issue list got there first, and named the workspace from
	// Linear's own slug — a different directory name for the same
	// branch. The PR has to find it by the branch, not by the name.
	f.git(f.repo, "fetch", "-q", "origin", head+":"+head)
	existing := filepath.Join(f.repo, ".worktrees.local", "bar-4157-something-else")
	f.git(f.repo, "worktree", "add", "-q", existing, head)

	out := f.open("42")

	if !strings.Contains(out, "bar-4157-something-else") {
		t.Errorf("did not join the workspace already open: %q", out)
	}
	if made := filepath.Join(f.repo, ".worktrees.local", head); f.exists(made) {
		t.Errorf("a second worktree on one branch: %s", made)
	}
	wts := strings.Count(f.git(f.repo, "worktree", "list", "--porcelain"), "\nworktree ")
	if wts != 1 { // the main tree plus one, and the count misses the first line
		t.Errorf("%d worktrees beside the main one, want 1", wts)
	}
}

func TestOpenYourOwnPRWithNoIssueKeepsThePRName(t *testing.T) {
	head := "refactor/tidy-imports"
	f := ownFixture(t, head)

	out := f.open("42")

	// No issue behind it, so the workspace stays a review's by name and
	// container — but the branch inside is still the real one, so a fix
	// committed while reviewing is on what you will push, and close
	// leaves it alone.
	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if !strings.Contains(out, "started reviews:"+name) {
		t.Errorf("output: %q", out)
	}
	if got := f.git(wt, "branch", "--show-current"); got != head {
		t.Errorf("worktree branch %q, want the PR's own %q", got, head)
	}
}

func TestCloseYourOwnPRPointsAtTheFeature(t *testing.T) {
	head := "bar-4157-drop-legacy-tenant-shim"
	f := ownFixture(t, head)
	f.open("42")

	var out strings.Builder
	err := runClose(f.cfg, []string{"42"}, &out)
	if err == nil {
		t.Fatalf("close removed a feature's workspace through the PR: %q", out.String())
	}
	// Not "already closed": the workspace is there, under the issue.
	if !strings.Contains(err.Error(), "owl issue close BAR-4157") {
		t.Errorf("close said %q, want it to name `owl issue close BAR-4157`", err)
	}
	if wt := filepath.Join(f.repo, ".worktrees.local", head); !f.exists(wt) {
		t.Errorf("the feature's worktree was removed anyway: %s", wt)
	}
	// And the window is still there, in the features container.
	if got := f.tmuxL("list-windows", "-t", mux.TmuxTarget(f.cfg.Issue.Session, ""), "-F", "#{window_name}"); !strings.Contains(got, head) {
		t.Errorf("features windows %q, want %s among them", got, head)
	}
}
