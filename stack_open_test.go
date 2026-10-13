package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// stackFixture is the fixture with its clone's remote naming acme/app
// — fetched, through insteadOf, from the local origin — and gh
// answering the repository's open pull requests from the page
// setGraph writes.
func stackFixture(t *testing.T, prs ...stackPR) *fixture {
	t.Helper()
	f := newFixture(t)
	t.Chdir(f.repo)
	f.git(f.repo, "remote", "set-url", "origin", "https://github.com/acme/app")
	f.git(f.repo, "config", "url."+f.origin+".insteadOf", "https://github.com/acme/app")
	f.write(filepath.Join(f.root, "bin", "gh"), "#!/bin/sh\ncat '"+filepath.Join(f.root, "graph.json")+"'\n")
	f.setGraph(prs...)
	return f
}

// setGraph is what GitHub says is open, from now on.
func (f *fixture) setGraph(prs ...stackPR) {
	f.t.Helper()
	f.write(filepath.Join(f.root, "graph.json"), repoGraphPage(f.t, "main", prs))
}

// lineOfTwo is #42 built on #7, both refs the fixture's origin has.
func lineOfTwo() []stackPR {
	tip := stackPROn(42, "seven", "fortytwo")
	tip.Title = "Fix: Crash on Startup!!"
	return []stackPR{stackPROn(7, "main", "seven"), tip}
}

func (f *fixture) record(name string) stackRecord {
	f.t.Helper()
	dir, err := stackRecordsDir(f.repo)
	if err != nil {
		f.t.Fatal(err)
	}
	var r stackRecord
	if err := json.Unmarshal(mustRead(f.t, filepath.Join(dir, name+".json")), &r); err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestOpenStackOpensTheLineAtItsTip(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"

	out := f.open("7", "--stack")

	if !strings.Contains(out, name+": #7 → #42\n") {
		t.Errorf("output does not show the line: %q", out)
	}
	// Detached at the tip's head: its tree holds both layers.
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if got := f.git(wt, "branch", "--show-current"); got != "" {
		t.Errorf("worktree on branch %q, want detached", got)
	}
	if got, want := f.git(wt, "rev-parse", "HEAD"), f.git(f.origin, "rev-parse", "refs/pull/42/head"); got != want {
		t.Errorf("worktree at %s, want the tip's head %s", got, want)
	}
	// The ref it was fetched into is gone again, and no branch was made.
	if refs := f.git(f.repo, "for-each-ref", "refs/owl"); refs != "" {
		t.Errorf("fetch ref left behind: %s", refs)
	}
	if br := f.branches(); !slices.Equal(br, []string{"main"}) {
		t.Errorf("branches %v, want only main", br)
	}
	if !slices.Contains(f.windows(), name) {
		t.Errorf("no window %s among %v", name, f.windows())
	}
	if got := promptIn(t, f.waitPane(name, "$(cat ")); got != "/owl:review 42 stack" {
		t.Errorf("prompt %q", got)
	}
	if r := f.record(name); r.Tip != 42 || !slices.Equal(r.PRs, []int{7, 42}) || r.Repo != "acme/app" {
		t.Errorf("record %+v", r)
	}
}

// TestOpenStackFindsItsWorkspaceAgain: any layer opens the line's one
// workspace, and a layer pushed on top of the tip extends the line
// rather than starting another — the record follows the tip.
func TestOpenStackFindsItsWorkspaceAgain(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"
	f.open("7", "--stack")

	grown := append(lineOfTwo(), stackPROn(43, "fortytwo", "fortythree"))
	f.setGraph(grown...)
	out := f.open("43", "--stack")

	if !strings.Contains(out, name+": #7 → #42 → #43\n") || strings.Contains(out, "fetching") {
		t.Errorf("re-open: %q", out)
	}
	if r := f.record(name); r.Tip != 43 || !slices.Equal(r.PRs, []int{7, 42, 43}) {
		t.Errorf("record %+v, want the tip moved to #43", r)
	}
	if n := strings.Count(strings.Join(f.windows(), "\n"), "stack-"); n != 1 {
		t.Errorf("%d stack windows: %v", n, f.windows())
	}
}

// TestOpenStackGivesAForkItsOwnWorkspace: a line that leaves the
// recorded one below its tip is another line, and the name its tip's
// title gives is taken, so it gets the tip's number.
func TestOpenStackGivesAForkItsOwnWorkspace(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	f.open("42", "--stack")

	other := stackPROn(8, "seven", "eight")
	other.Title = "fix: crash on startup"
	f.setGraph(append(lineOfTwo(), other)...)
	// The bottom is below the fork now: which line is not one answer.
	var fe forkError
	if err := runOpen(f.cfg, []string{"7", "--stack"}, &strings.Builder{}, true); !errors.As(err, &fe) {
		t.Fatalf("open 7 --stack below the fork: %v, want a forkError", err)
	} else if msg := err.Error(); !strings.Contains(msg, "owl pr open 8 --stack") || !strings.Contains(msg, "owl pr open 42 --stack") {
		t.Errorf("the fork's error does not name both tips:\n%s", msg)
	}
	// The other line needs refs/pull/8/head on the origin.
	f.git(f.origin, "update-ref", "refs/pull/8/head", "refs/pull/7/head")
	out := f.open("8", "--stack")
	if !strings.Contains(out, "stack-8: #7 → #8\n") {
		t.Errorf("the fork's line: %q", out)
	}
	if r := f.record("stack-fix-crash-on-startup"); r.Tip != 42 {
		t.Errorf("the first line's record moved: %+v", r)
	}
}

func TestOpenStackRefusesALonePR(t *testing.T) {
	f := stackFixture(t, stackPROn(42, "main", "fortytwo"))
	err := runOpen(f.cfg, []string{"42", "--stack"}, &strings.Builder{}, true)
	if err == nil || !strings.Contains(err.Error(), "#42 is not stacked") {
		t.Errorf("open --stack on a lone PR: %v", err)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local")) {
		t.Error("a worktree was made for it anyway")
	}
}

func TestStackNamesAreWorkspaces(t *testing.T) {
	for _, name := range []string{"stack-fix-crash", "stack-4974"} {
		if !isWorkspaceName(name) || !reviews.owns(Config{}, name) {
			t.Errorf("%s is not a review workspace", name)
		}
		if key := issueKeyOf(name); key != "" {
			t.Errorf("%s reads as issue %s", name, key)
		}
		if sc := scopeForName(name); sc.name != "reviews" {
			t.Errorf("%s lands in %s", name, sc.name)
		}
	}
	if isStackName("stack-") || isStackName("stack-Upper") || isStackName("xstack-a") {
		t.Error("isStackName accepts what open never names")
	}
}
