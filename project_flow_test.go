package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

// TestProjectOpenReadsTheStateUnderTheLock: two first opens of one
// project at once — a key pressed twice — must agree on one session.
// The second waits on the lock while the first saves its session id,
// and has to read the state after the wait, not before it: a session
// read before would be empty, and the second would write its own over
// the first's, whose transcript is the one on disk.
func TestProjectOpenReadsTheStateUnderTheLock(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	unlock, err := lockWorkspace(f.repo, "proj-sequential-capture-redesign")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runProject(f.cfg, []string{"open", "a76d38ca8527"}, io.Discard) }()
	time.Sleep(500 * time.Millisecond) // long enough to have read the state and reached the lock
	first := projectState{Session: "11111111-1111-4111-8111-111111111111", Name: "Sequential Capture redesign"}
	if err := saveProjectState("uuid-a76d38ca8527", first); err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := loadProjectStates()["uuid-a76d38ca8527"]; got.Session != first.Session {
		t.Errorf("session = %q, want the first open's %q", got.Session, first.Session)
	}
}

// argsAgent makes the agent a script that writes each argument it is
// started with on a line of its own, and returns the file.
func argsAgent(t *testing.T, f *fixture) string {
	t.Helper()
	got := filepath.Join(f.root, "agent.args")
	script := filepath.Join(f.root, "agent-args.sh")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(got) + "\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	f.cfg.Agent.Cmd = shellQuote(script)
	return got
}

// waitArgs waits for the agent to have started and returns its
// arguments.
func waitArgs(t *testing.T, path string) []string {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		}
	}
	t.Fatalf("the agent never started")
	return nil
}

// seedConversation puts a transcript named for session where Claude
// keeps the conversations of the worktree at wt.
func seedConversation(t *testing.T, wt, session string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", encodeProjectPath(wt))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, session+".jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProjectCloseTakesOneWorkspace: with a worktree under each of a
// project's names — left by the open that used to make a second one
// after a rename — close removes the workspace open would use, window
// and worktree together, and leaves the other.
func TestProjectCloseTakesOneWorkspace(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if err := saveProjectState("uuid-a76d38ca8527", projectState{Session: "0c3e2a4b-7d1f-4e8a-9b6c-5f2d1a0e3c47", Name: "Zebra capture"}); err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(f.repo, ".worktrees.local", "proj-sequential-capture-redesign")
	first := filepath.Join(f.repo, ".worktrees.local", "proj-zebra-capture")
	f.git(f.repo, "worktree", "add", "--detach", current, "origin/main") // listed first, by age and by name
	f.git(f.repo, "worktree", "add", "--detach", first, "origin/main")

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "projects:proj-zebra-capture") {
		t.Fatalf("open: %q", out.String())
	}
	out.Reset()
	if err := runProject(f.cfg, []string{"close", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !f.exists(current) || f.exists(first) {
		t.Errorf("close removed the wrong worktree: current exists %v, first exists %v\n%s", f.exists(current), f.exists(first), out.String())
	}
}

// TestProjectOpenLeavesAnotherProjectsWorkspace: a project renamed away
// from a name keeps its workspace under it, so a second project later
// given that name must not walk into the first one's worktree and
// conversation.
func TestProjectOpenLeavesAnotherProjectsWorkspace(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// Raw-data JSON ingest was first opened as Sequential Capture redesign.
	other := projectState{Session: "22222222-2222-4222-8222-222222222222", Name: "Sequential Capture redesign"}
	if err := saveProjectState("uuid-eb528db32d42", other); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(f.repo, ".worktrees.local", "proj-sequential-capture-redesign")
	f.git(f.repo, "worktree", "add", "--detach", theirs, "origin/main")
	seedConversation(t, theirs, other.Session)
	args := argsAgent(t, f)

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "projects:proj-sequential-capture-redesign\n") || strings.Contains(out.String(), "projects:proj-sequential-capture-redesign,") {
		t.Errorf("open took the other project's workspace: %q", out.String())
	}
	if got := waitArgs(t, args); got[0] != "--session-id" {
		t.Errorf("agent args %v, want a fresh conversation", got)
	}
}

// TestProjectResumesOnlyItsOwnConversation: a transcript in the
// worktree's conversation directory is not this project's unless it
// carries its session id; resuming an id with no transcript fails.
func TestProjectResumesOnlyItsOwnConversation(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	wt := filepath.Join(f.repo, ".worktrees.local", "proj-sequential-capture-redesign")
	seedConversation(t, wt, "33333333-3333-4333-8333-333333333333") // someone else's, in the same directory
	args := argsAgent(t, f)

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	st := loadProjectStates()["uuid-a76d38ca8527"]
	got := waitArgs(t, args)
	if len(got) < 2 || got[0] != "--session-id" || got[1] != st.Session {
		t.Errorf("agent args %v, want --session-id %s", got, st.Session)
	}
	if strings.Contains(out.String(), "resuming") {
		t.Errorf("open says it resumes: %q", out.String())
	}
}

// TestSaveProjectStateConcurrently: two projects opened at once each
// save their own entry; neither may be lost to the other's write.
func TestSaveProjectStateConcurrently(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	const n = 24
	errs := make(chan error, n)
	for i := range n {
		go func() {
			errs <- saveProjectState(fmt.Sprintf("uuid-%02d", i), projectState{Session: fmt.Sprintf("s-%02d", i), Name: "p"})
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := len(loadProjectStates()); got != n {
		t.Errorf("%d of %d entries survived", got, n)
	}
}

// TestProjectOpenFindsItsConversationUnderAnyName: renamed twice, from
// a state from before owl recorded the workspace, and moved by hand to
// the middle name — neither the first name nor the current one is where
// the conversation is, but its transcript says which directory is.
func TestProjectOpenFindsItsConversationUnderAnyName(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	st := projectState{Session: "44444444-4444-4444-8444-444444444444", Name: "Capture redesign draft"}
	if err := saveProjectState("uuid-a76d38ca8527", st); err != nil {
		t.Fatal(err)
	}
	middle := filepath.Join(f.repo, ".worktrees.local", "proj-capture-redesign-v2")
	seedConversation(t, middle, st.Session)
	args := argsAgent(t, f)

	var out strings.Builder
	if err := runProject(f.cfg, []string{"open", "a76d38ca8527"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "creating "+middle+" ") {
		t.Errorf("open: %q", out.String())
	}
	if got := waitArgs(t, args); got[0] != "--resume" || got[1] != st.Session {
		t.Errorf("agent args %v, want --resume %s", got, st.Session)
	}
	if rec := loadProjectStates()["uuid-a76d38ca8527"]; rec.Workspace != "proj-capture-redesign-v2" || rec.Session != st.Session {
		t.Errorf("state after open: %+v, want the workspace recorded", rec)
	}

	// Its worktree there now, a later open finds it rather than making
	// another.
	out.Reset()
	if err := runProject(f.cfg, []string{"start", "a76d38ca8527"}, &out); err != nil || strings.Contains(out.String(), "creating") {
		t.Errorf("second open: %v, %q", err, out.String())
	}
}
