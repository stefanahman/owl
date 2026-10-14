package main

import (
	"encoding/json"
	"errors"
	"os"
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

// TestCloseStackKeepsItsRecord: close removes the worktree and the
// window and keeps the record, so the line opens again under its name
// and Claude resumes the conversation stored under it.
func TestCloseStackKeepsItsRecord(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"
	f.open("42", "--stack")

	var out strings.Builder
	if err := runClose(f.cfg, []string{"--stack", "7"}, &out); err != nil {
		t.Fatalf("close --stack 7: %v\n%s", err, out.String())
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", name)) || slices.Contains(f.windows(), name) {
		t.Errorf("still there after close: %v", f.windows())
	}
	if r := f.record(name); r.Tip != 42 {
		t.Errorf("record after close: %+v", r)
	}
	var nothing nothingToCloseError
	if err := runClose(f.cfg, []string{"--stack", "7"}, &out); !errors.As(err, &nothing) {
		t.Errorf("second close: %v, want nothing to close", err)
	}
	if err := runClose(f.cfg, []string{"--stack", "99"}, &out); !errors.As(err, &nothing) {
		t.Errorf("close of a PR in no stack: %v, want nothing to close", err)
	}
	if !strings.Contains(f.open("7", "--stack"), name+": ") {
		t.Error("re-open after close took another name")
	}
}

// TestCloseStackFromInside: without a number, the stack is the one
// whose worktree the caller stands in.
func TestCloseStackFromInside(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"
	f.open("42", "--stack")
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	t.Chdir(wt)
	if err := runClose(f.cfg, []string{"--stack"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("worktree still there: %v", err)
	}
}

// TestCloseStackFollowsTheLine: close finds a workspace as open does,
// by its tip on the line as it is now. A layer pushed on top of the
// tip since the last open is on no record yet, and closing the line
// by it still closes the workspace; so does its name.
func TestCloseStackFollowsTheLine(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"
	f.open("42", "--stack")
	f.setGraph(append(lineOfTwo(), stackPROn(43, "fortytwo", "fortythree"))...)

	var out strings.Builder
	if err := runClose(f.cfg, []string{"--stack", "43"}, &out); err != nil {
		t.Fatalf("close --stack 43 on a line grown past the record: %v", err)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", name)) {
		t.Error("the worktree is still there")
	}

	// Reopened, the worktree is made again at the line's tip, now #43.
	f.git(f.origin, "update-ref", "refs/pull/43/head", "refs/pull/42/head")
	f.open("7", "--stack")
	if err := runClose(f.cfg, []string{"--stack", name}, &out); err != nil {
		t.Fatalf("close --stack by name: %v", err)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", name)) {
		t.Error("the worktree is still there after close by name")
	}
}

func TestStackNamesAreWorkspaces(t *testing.T) {
	for _, name := range []string{"stack-fix-crash", "stack-1206"} {
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

// TestCloseStackRefusesASharedLayer: two lines that share a layer are
// two workspaces, and the shared layer names neither of them — by the
// line as it is now, and by the records alone when GitHub is out of
// reach.
func TestCloseStackRefusesASharedLayer(t *testing.T) {
	records := []stackRecord{
		{name: "stack-ui", Repo: "acme/app", Tip: 1206, PRs: []int{1201, 1202, 1203, 1204, 1205, 1206}},
		{name: "stack-export", Repo: "acme/app", Tip: 1210, PRs: []int{1201, 1202, 1203, 1204, 1205, 1210}},
		{name: "stack-elsewhere", Repo: "acme/other", Tip: 9, PRs: []int{1205, 9}},
	}
	g := newPRGraph("acme/app", "main", forkedStackPRs())
	for _, graph := range []*prGraph{g, nil} {
		_, err := stackToClose(records, graph, "acme/app", "/repo", 1205)
		if err == nil || !strings.Contains(err.Error(), "stack-export (to #1210), stack-ui (to #1206)") || strings.Contains(err.Error(), "elsewhere") {
			t.Errorf("graph %v: stackToClose(1205) = %v, want both of acme/app's lines named", graph != nil, err)
		}
		if name, err := stackToClose(records, graph, "acme/app", "/repo", 1210); err != nil || name != "stack-export" {
			t.Errorf("graph %v: stackToClose(1210) = %q, %v", graph != nil, name, err)
		}
	}
	// The remote's spelling of the repository need not be GitHub's.
	if name, err := stackToClose(records, g, "Acme/App", "/repo", 1206); err != nil || name != "stack-ui" {
		t.Errorf("stackToClose(1206) under Acme/App = %q, %v", name, err)
	}
}

// TestOpenStackResumesAMergedLine: once the line has merged GitHub
// holds none of it open, and opening it by any layer goes back to the
// workspace that reviewed it, as its record has the line. Nothing is
// fetched and the record does not move. Closed, there is nothing left
// to go back to, and nothing is made.
func TestOpenStackResumesAMergedLine(t *testing.T) {
	f := stackFixture(t, lineOfTwo()...)
	name := "stack-fix-crash-on-startup"
	f.open("42", "--stack")
	f.setGraph()

	out := f.open("7", "--stack")
	if !strings.Contains(out, name+": #7 → #42, no longer open\n") || strings.Contains(out, "fetching") {
		t.Errorf("open of the merged line: %q", out)
	}
	if r := f.record(name); r.Tip != 42 || !slices.Equal(r.PRs, []int{7, 42}) {
		t.Errorf("record %+v, want it as the last open left it", r)
	}

	if err := runClose(f.cfg, []string{"--stack", "42"}, &strings.Builder{}); err != nil {
		t.Fatal(err)
	}
	err := runOpen(f.cfg, []string{"42", "--stack"}, &strings.Builder{}, true)
	if err == nil || !strings.Contains(err.Error(), "not an open pull request") {
		t.Errorf("open of a merged line no workspace holds: %v", err)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", name)) {
		t.Error("a worktree was made for a merged line")
	}
}
