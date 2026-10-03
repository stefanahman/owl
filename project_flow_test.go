package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stefanahman/mux"
)

// projectWindows lists the projects session's windows, keepalive
// included.
func (f *fixture) projectWindows() []string {
	out, _ := tmux("list-windows", "-t", mux.TmuxTarget(f.cfg.Project.Session, ""), "-F", "#{window_name}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func TestProjectOpenCreatesTheConversation(t *testing.T) {
	f, fake := issueFixture(t)
	t.Chdir(f.repo)
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	// The fake serves Sequential Capture redesign under this slug.
	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	// One lookup by id, and not the filtered list of every project the
	// user works in: that query has been measured at eighteen seconds.
	if fake.projectLookups != 1 {
		t.Errorf("project lookups = %d, want 1", fake.projectLookups)
	}
	for _, q := range fake.queries {
		if strings.Contains(q, "projects(first:") {
			t.Errorf("open by id fetched the whole project list:\n%s", q)
		}
	}
	name := "proj-sequential-capture-redesign"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if !strings.Contains(out.String(), "creating "+wt+" detached at origin/main") || !strings.Contains(out.String(), "started projects:"+name) {
		t.Errorf("output: %q", out.String())
	}
	// Detached, because origin/main is checked out in the main worktree
	// already and a project's agent has nothing to commit here.
	if branch := f.git(wt, "branch", "--show-current"); branch != "" {
		t.Errorf("the project worktree is on branch %q, want detached", branch)
	}
	if head, want := f.git(wt, "rev-parse", "HEAD"), f.git(f.repo, "rev-parse", "origin/main"); head != want {
		t.Errorf("worktree HEAD %s, want origin/main %s", head, want)
	}
	if got, want := f.projectWindows(), []string{"scratch", name}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows %v, want %v", got, want)
	}

	// owl gave the conversation its id and kept it.
	states := map[string]projectState{}
	b, err := os.ReadFile(filepath.Join(state, "owl", "projects.json"))
	if err != nil {
		t.Fatalf("no project state: %v", err)
	}
	if err := json.Unmarshal(b, &states); err != nil {
		t.Fatal(err)
	}
	st, ok := states["uuid-a76d38ca8527"]
	if !ok || len(st.Session) != 36 || st.Name != "Sequential Capture redesign" {
		t.Fatalf("project state = %+v", states)
	}

	// Opening again finds the window and keeps the same id: the
	// conversation is the project's, not one per visit.
	out.Reset()
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "selected projects:"+name) {
		t.Errorf("second open: %q", out.String())
	}
	if again := loadProjectStates()["uuid-a76d38ca8527"]; again.Session != st.Session {
		t.Errorf("session id changed from %q to %q", st.Session, again.Session)
	}
	if got := f.projectWindows(); len(got) != 2 {
		t.Errorf("second open added a window: %v", got)
	}

	// A name fragment resolves too, so `owl project open sequential`
	// works from a terminal.
	out.Reset()
	if err := runProject(f.cfg, []string{"start", "sequential"}, &out); err != nil {
		t.Errorf("open by name fragment: %v", err)
	}

	// close removes the worktree and the window; the id survives, so a
	// later open resumes rather than starting cold.
	out.Reset()
	if err := runProject(f.cfg, []string{"close", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"removed worktree " + wt, "closed window projects:" + name} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("close output lacks %q:\n%s", want, out.String())
		}
	}
	if f.exists(wt) {
		t.Error("the worktree is still there")
	}
	if kept := loadProjectStates()["uuid-a76d38ca8527"]; kept.Session != st.Session {
		t.Errorf("close forgot the session id: %+v", kept)
	}
}

func TestProjectOpenRefusesAnAmbiguousName(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// Both fixture projects contain an "a"; owl names them rather than
	// opening the wrong conversation.
	err := runProject(f.cfg, []string{"open", "a"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "matches 2 projects") {
		t.Errorf("ambiguous name: %v", err)
	}
	if err := runProject(f.cfg, []string{"open", "nothing at all"}, io.Discard); err == nil || !strings.Contains(err.Error(), "no project matches") {
		t.Errorf("unknown name: %v", err)
	}
}

// seedRenamed records the fixture project as first opened under an
// older name, as it is after a rename in Linear, and gives the
// worktree at name a conversation on disk.
func seedRenamed(t *testing.T, f *fixture, name string) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := saveProjectState("uuid-a76d38ca8527", projectState{Session: "0c3e2a4b-7d1f-4e8a-9b6c-5f2d1a0e3c47", Name: "Capture redesign draft"}); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	conv := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", encodeProjectPath(wt))
	if err := os.MkdirAll(conv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(conv, "0c3e2a4b-7d1f-4e8a-9b6c-5f2d1a0e3c47.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return wt
}

// nameAgent makes the agent a script that writes the value of its
// --name argument to a file, as the shell handed it over, and returns
// the file.
func nameAgent(t *testing.T, f *fixture) string {
	t.Helper()
	got := filepath.Join(f.root, "agent.name")
	script := filepath.Join(f.root, "agent.sh")
	body := "#!/bin/sh\nwhile [ $# -gt 0 ]; do\n\tif [ \"$1\" = --name ]; then printf '%s\\n' \"$2\" > " + shellQuote(got) + "; fi\n\tshift\ndone\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	f.cfg.Agent.Cmd = shellQuote(script)
	return got
}

func TestProjectOpenKeepsARenamedProjectsWorkspace(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	// Opened as "Capture redesign draft", renamed in Linear since: the
	// fake serves it as Sequential Capture redesign.
	first := "proj-capture-redesign-draft"
	wt := seedRenamed(t, f, first)
	f.git(f.repo, "worktree", "add", "--detach", wt, "origin/main")
	named := nameAgent(t, f)

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	// The workspace keeps the first name; the conversation is named for
	// the project as it is called now, as one argument.
	f.waitFile(named, "Sequential Capture redesign\n")
	// The worktree it already has, and the conversation in it: a second
	// one under the new name would start that conversation over.
	if strings.Contains(out.String(), "creating") || !strings.Contains(out.String(), "started projects:"+first+", resuming the conversation") {
		t.Errorf("open: %q", out.String())
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", "proj-sequential-capture-redesign")) {
		t.Error("open made a second worktree under the new name")
	}
	if got, want := f.projectWindows(), []string{"scratch", first}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows %v, want %v", got, want)
	}

	// The name in the window list resolves, though it no longer matches
	// the project's name.
	out.Reset()
	if err := runProject(f.cfg, []string{"start", "capture-redesign-draft"}, &out); err != nil || out.String() != "ready projects:"+first+"\n" {
		t.Errorf("open by the first name: %v, %q", err, out.String())
	}

	out.Reset()
	if err := runProject(f.cfg, []string{"close", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"removed worktree " + wt, "closed window projects:" + first} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("close output lacks %q:\n%s", want, out.String())
		}
	}

	// Closed, then opened again: the worktree comes back under the name
	// Claude keeps the conversation by, and the conversation resumes.
	out.Reset()
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "creating "+wt+" detached at origin/main") || !strings.Contains(out.String(), "resuming the conversation") {
		t.Errorf("reopen: %q", out.String())
	}
}

// A worktree renamed by hand to the project's current name is found
// there, though the state still holds the first.
func TestProjectOpenFindsAWorktreeUnderItsCurrentName(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	current := "proj-sequential-capture-redesign"
	wt := seedRenamed(t, f, current)
	f.git(f.repo, "worktree", "add", "--detach", wt, "origin/main")

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "creating") || !strings.Contains(out.String(), "started projects:"+current+", resuming the conversation") {
		t.Errorf("open: %q", out.String())
	}
	out.Reset()
	if err := runProject(f.cfg, []string{"close", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "removed worktree "+wt) {
		t.Errorf("close: %q", out.String())
	}

	// Closed, then opened again: the conversation was moved with the
	// worktree, so the worktree comes back under the current name, not
	// the first, or the conversation would be left behind.
	out.Reset()
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "creating "+wt+" detached at origin/main") || !strings.Contains(out.String(), "resuming the conversation") {
		t.Errorf("reopen: %q", out.String())
	}
}
