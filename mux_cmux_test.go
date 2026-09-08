package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestMain lets the test binary stand in for the cmux CLI: the tests
// put a symlink named cmux to it on PATH, and a run under that name
// serves the fake below instead of the tests.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "cmux" {
		os.Exit(fakeCmuxMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// fakeCmuxState is what the fake cmux knows, kept in a file between
// invocations: the shapes the adapter reads, as cmux 0.64.22 prints
// them, and what it was asked.
type fakeCmuxState struct {
	Next          int
	Workspaces    []cmuxWorkspace
	Surfaces      map[string]cmuxSurface // workspace ref → its terminal
	Sessions      []cmuxSession
	Notifications []fakeCmuxNote
	Top           map[string]fakeCmuxTop // workspace ref → what runs there
	Typed         map[string][]string    // surface ref → text and keys, in order
	Calls         []string
	Selected      string
	Focused       string
}

type fakeCmuxNote struct {
	Workspace string `json:"workspace_id"`
	Read      bool   `json:"is_read"`
}

type fakeCmuxTop struct {
	Agents    []string // coding agents cmux detected
	Processes []string // process names attributed to the terminal
}

func fakeCmuxStatePath() string { return filepath.Join(os.Getenv("PR_OWL_FAKE_CMUX"), "state.json") }

func loadFakeCmux() fakeCmuxState {
	var st fakeCmuxState
	if data, err := os.ReadFile(fakeCmuxStatePath()); err == nil {
		_ = json.Unmarshal(data, &st)
	}
	if st.Surfaces == nil {
		st.Surfaces = map[string]cmuxSurface{}
	}
	if st.Top == nil {
		st.Top = map[string]fakeCmuxTop{}
	}
	if st.Typed == nil {
		st.Typed = map[string][]string{}
	}
	return st
}

func saveFakeCmux(st fakeCmuxState) {
	data, _ := json.Marshal(st)
	_ = os.WriteFile(fakeCmuxStatePath(), data, 0o644)
}

// fakeCmuxMain is the CLI: the verbs the adapter uses, with cmux's
// option spelling, answering from the state file.
func fakeCmuxMain(args []string) int {
	st := loadFakeCmux()
	st.Calls = append(st.Calls, strings.Join(args, " "))
	defer func() { saveFakeCmux(st) }()

	// Global presentation flags first, then the verb, then --opt value
	// pairs and positionals.
	opts := map[string]string{}
	var words []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
		case strings.HasPrefix(a, "--") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--"):
			opts[a] = args[i+1]
			i++
		case strings.HasPrefix(a, "--"): // a flag without a value, --processes
			opts[a] = "true"
		default:
			words = append(words, a)
		}
	}
	fail := func(msg string) int { fmt.Fprintln(os.Stderr, "cmux: "+msg); return 1 }
	find := func(ref string) (int, bool) {
		for i, w := range st.Workspaces {
			if w.Ref == ref || w.ID == ref {
				return i, true
			}
		}
		return 0, false
	}
	out := func(v any) int {
		data, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(data))
		return 0
	}
	verb := strings.Join(words, " ")
	switch {
	case verb == "ping":
		fmt.Println("PONG")
	case verb == "workspace list":
		return out(map[string]any{"window_ref": "window:1", "workspaces": st.Workspaces})
	case verb == "workspace create":
		st.Next++
		w := cmuxWorkspace{ID: fmt.Sprintf("WS-%d", st.Next), Ref: fmt.Sprintf("workspace:%d", st.Next), Title: opts["--name"], CustomTitle: opts["--name"], HasCustom: opts["--name"] != ""}
		st.Workspaces = append(st.Workspaces, w)
		st.Surfaces[w.Ref] = cmuxSurface{ID: fmt.Sprintf("SF-%d", st.Next), Ref: fmt.Sprintf("surface:%d", st.Next)}
		fmt.Println("OK " + w.Ref)
	case verb == "workspace select", verb == "workspace close", verb == "mark-notification-read", verb == "list-pane-surfaces", strings.HasPrefix(verb, "top"):
		i, ok := find(opts["--workspace"])
		if !ok {
			return fail("no such workspace " + opts["--workspace"])
		}
		w := st.Workspaces[i]
		switch verb {
		case "workspace select":
			st.Selected = w.Ref
			fmt.Println("OK " + w.Ref)
		case "workspace close":
			st.Workspaces = append(st.Workspaces[:i], st.Workspaces[i+1:]...)
			fmt.Println("OK " + w.Ref)
		case "mark-notification-read":
			for j := range st.Notifications {
				if st.Notifications[j].Workspace == w.ID {
					st.Notifications[j].Read = true
				}
			}
			fmt.Println("OK")
		case "list-pane-surfaces":
			return out(map[string]any{"workspace_ref": w.Ref, "surfaces": []cmuxSurface{st.Surfaces[w.Ref]}})
		default: // top
			top := st.Top[w.Ref]
			if opts["--format"] == "tsv" {
				sf := st.Surfaces[w.Ref].Ref
				fmt.Printf("0.0\t1\t1\tworkspace\t%s\twindow:1\t%s\n", w.Ref, w.label())
				for i, p := range top.Processes {
					fmt.Printf("0.0\t1\t1\tprocess\t%d\t%s\t%s\n", 1000+i, sf, p)
				}
				return 0
			}
			agents := []map[string]string{}
			for _, a := range top.Agents {
				agents = append(agents, map[string]string{"id": a})
			}
			return out(map[string]any{"coding_agents": agents})
		}
	case len(words) == 2 && (words[0] == "send" || words[0] == "send-key"):
		sf := opts["--surface"]
		if sf == "" {
			return fail(words[0] + " needs --surface")
		}
		if words[0] == "send-key" {
			st.Typed[sf] = append(st.Typed[sf], "<"+words[1]+">")
		} else {
			st.Typed[sf] = append(st.Typed[sf], words[1])
		}
		fmt.Println("OK " + sf)
	case verb == "sessions":
		return out(map[string]any{"sessions": st.Sessions, "total_matches": len(st.Sessions)})
	case verb == "list-notifications":
		return out(st.Notifications)
	case verb == "focus-window":
		st.Focused = opts["--window"]
		fmt.Println("OK " + st.Focused)
	case verb == "notify":
		fmt.Println("OK note")
	default:
		return fail("unknown command " + verb)
	}
	return 0
}

// fakeCmux seeds and reads the fake's state from the test side.
type fakeCmux struct {
	t   *testing.T
	dir string
}

// newFakeCmux puts the fake first on PATH and clears the variables a
// cmux terminal would carry, so the test decides what is "inside".
func newFakeCmux(t *testing.T) *fakeCmux {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "cmux")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PR_OWL_FAKE_CMUX", dir)
	t.Setenv("CMUX_WORKSPACE_ID", "")
	t.Setenv("CMUX_SURFACE_ID", "")
	t.Setenv("HERDR_ENV", "")
	saveFakeCmux(fakeCmuxState{})
	return &fakeCmux{t: t, dir: dir}
}

func (f *fakeCmux) state() fakeCmuxState { return loadFakeCmux() }

func (f *fakeCmux) edit(fn func(st *fakeCmuxState)) {
	st := loadFakeCmux()
	fn(&st)
	saveFakeCmux(st)
}

func (f *fakeCmux) workspace(label string) (cmuxWorkspace, bool) {
	for _, w := range f.state().Workspaces {
		if w.label() == label {
			return w, true
		}
	}
	return cmuxWorkspace{}, false
}

// typed is what was sent to the workspace's terminal, text and keys.
func (f *fakeCmux) typed(label string) []string {
	w, ok := f.workspace(label)
	if !ok {
		return nil
	}
	st := f.state()
	return st.Typed[st.Surfaces[w.Ref].Ref]
}

func (f *fakeCmux) calls() []string { return f.state().Calls }

func (f *fakeCmux) addWorkspace(label, id string) {
	f.edit(func(st *fakeCmuxState) {
		st.Next++
		w := cmuxWorkspace{ID: id, Ref: fmt.Sprintf("workspace:%d", st.Next), Title: label, CustomTitle: label, HasCustom: label != ""}
		if label == "" {
			w.Title = "~"
		}
		st.Workspaces = append(st.Workspaces, w)
		st.Surfaces[w.Ref] = cmuxSurface{ID: "SF-" + id, Ref: fmt.Sprintf("surface:%d", st.Next)}
	})
}

func (f *fakeCmux) addSession(workspaceID, lifecycle string, live bool, updated string) {
	f.edit(func(st *fakeCmuxState) {
		st.Sessions = append(st.Sessions, cmuxSession{Workspace: workspaceID, Lifecycle: lifecycle, PIDExists: live, UpdatedAt: updated})
	})
}

func (f *fakeCmux) addNote(workspaceID string, read bool) {
	f.edit(func(st *fakeCmuxState) {
		st.Notifications = append(st.Notifications, fakeCmuxNote{Workspace: workspaceID, Read: read})
	})
}

func (f *fakeCmux) setTop(label string, agents, processes []string) {
	w, ok := f.workspace(label)
	if !ok {
		f.t.Fatalf("no workspace %q", label)
	}
	f.edit(func(st *fakeCmuxState) { st.Top[w.Ref] = fakeCmuxTop{Agents: agents, Processes: processes} })
}

func TestCmuxOpenCreatesAWorkspaceAndTypesTheStartLine(t *testing.T) {
	fake := newFakeCmux(t)
	fake.addWorkspace("", "HOME")
	m := cmuxMux{}
	if err := m.Prepare("/repo"); err != nil {
		t.Fatal(err)
	}
	if err := m.Open("pr-42-fix", "/repo/.worktrees.local/pr-42-fix", "claude '/pr-review:pr-review 42'"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.calls()[1], "workspace create --name pr-42-fix --cwd /repo/.worktrees.local/pr-42-fix --focus false"; got != want {
		t.Errorf("create call = %q, want %q", got, want)
	}
	// cmux 0.64.22 gives no CMUX_SURFACE_ID: the line carries it for
	// the wrapper.
	if got, want := fake.typed("pr-42-fix"), []string{"CMUX_SURFACE_ID=SF-2 claude '/pr-review:pr-review 42'", "<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	if got, want := m.Windows(), []string{"pr-42-fix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Windows() = %v, want %v (the home workspace is not a review)", got, want)
	}
	// A cmux that names the surface itself gets the line as it is.
	t.Setenv("CMUX_SURFACE_ID", "given")
	if err := m.Run("pr-42-fix", "claude -c"); err != nil {
		t.Fatal(err)
	}
	if got := fake.typed("pr-42-fix"); got[len(got)-2] != "claude -c" {
		t.Errorf("typed %v, want the bare line last", got)
	}
}

func TestCmuxStates(t *testing.T) {
	fake := newFakeCmux(t)
	for i, label := range []string{"pr-1-working", "pr-2-blocked", "pr-3-done", "pr-4-idle", "pr-5-stale", "pr-6-none", "scratch"} {
		fake.addWorkspace(label, fmt.Sprintf("W%d", i+1))
	}
	fake.addSession("W1", "running", true, "2026-09-08T16:00:00Z")
	fake.addSession("W2", "needsInput", true, "2026-09-08T16:00:00Z")
	fake.addSession("W3", "idle", true, "2026-09-08T16:00:00Z")
	fake.addSession("W4", "idle", true, "2026-09-08T16:00:00Z")
	fake.addSession("W5", "needsInput", false, "2026-09-08T16:00:00Z") // the agent exited; cmux keeps the record
	// An older, exited session of W1 must not shadow the live one.
	fake.addSession("W1", "idle", false, "2026-09-08T15:00:00Z")
	fake.addNote("W3", false)
	fake.addNote("W4", true)
	want := map[string]string{
		"pr-1-working": agentWorking, "pr-2-blocked": agentBlocked, "pr-3-done": agentDone,
		"pr-4-idle": agentIdle, "pr-5-stale": "", "pr-6-none": "",
	}
	if got := (cmuxMux{}).States(); !reflect.DeepEqual(got, want) {
		t.Errorf("States() = %v, want %v", got, want)
	}
}

func TestCmuxAtShell(t *testing.T) {
	fake := newFakeCmux(t)
	fake.addWorkspace("pr-7-x", "W7")
	m := cmuxMux{}
	cases := []struct {
		agents, processes []string
		want              bool
	}{
		{nil, nil, true},                         // an idle shell top may not list at all
		{nil, []string{"zsh"}, true},             // the login shell
		{nil, []string{"2.1.263", "zsh"}, false}, // Claude's process is named after its version
		{[]string{"claude"}, []string{"zsh"}, false},
	}
	for _, c := range cases {
		fake.setTop("pr-7-x", c.agents, c.processes)
		if got := m.AtShell("pr-7-x"); got != c.want {
			t.Errorf("AtShell with agents %v, processes %v = %v, want %v", c.agents, c.processes, got, c.want)
		}
	}
	if m.AtShell("pr-8-missing") {
		t.Error("an unknown workspace is not at a shell")
	}
}

func TestCmuxSelectCloseCurrentNotify(t *testing.T) {
	fake := newFakeCmux(t)
	fake.addWorkspace("pr-9-x", "W9")
	fake.addNote("W9", false)
	m := cmuxMux{}
	if err := m.Select("pr-9-x"); err != nil {
		t.Fatal(err)
	}
	st := fake.state()
	if st.Selected != "workspace:1" || !st.Notifications[0].Read {
		t.Errorf("after Select: selected %q, notification read %v", st.Selected, st.Notifications[0].Read)
	}
	if hint := m.AttachHint(); hint != "cmux" {
		t.Errorf("AttachHint outside cmux = %q", hint)
	}
	if err := m.SwitchClient(); err != nil || fake.state().Focused != "window:1" {
		t.Errorf("SwitchClient outside cmux: err %v, focused %q", err, fake.state().Focused)
	}
	if _, ok := m.Current(); ok {
		t.Error("Current outside cmux should be unknown")
	}
	t.Setenv("CMUX_WORKSPACE_ID", "W9")
	if name, ok := m.Current(); !ok || name != "pr-9-x" {
		t.Errorf("Current inside the workspace = %q, %v", name, ok)
	}
	if hint := m.AttachHint(); hint != "" {
		t.Errorf("AttachHint inside cmux = %q", hint)
	}
	if err := m.SwitchClient(); err != nil {
		t.Errorf("SwitchClient inside cmux: %v", err)
	}
	m.Notify("all done")
	calls := fake.calls()
	if got, want := calls[len(calls)-1], "notify --title pr-owl --body all done --workspace W9"; got != want {
		t.Errorf("notify call = %q, want %q", got, want)
	}
	if err := m.Close("pr-9-x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.workspace("pr-9-x"); ok {
		t.Error("workspace still there after Close")
	}
	if err := m.Close("pr-9-x"); err == nil || !strings.Contains(err.Error(), `no workspace "pr-9-x"`) {
		t.Errorf("Close of a closed workspace: %v", err)
	}
}

func TestCmuxUnreachableNamesIt(t *testing.T) {
	newFakeCmux(t)
	t.Setenv("PR_OWL_FAKE_CMUX", "") // the fake cannot find its state: every call fails
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cmux"), []byte("#!/bin/sh\necho 'cmux: socket not found' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := cmuxMux{}.Prepare("/repo")
	if err == nil || !strings.Contains(err.Error(), "socket not found") || !strings.Contains(err.Error(), "CMUX_SOCKET_MODE=allowAll") {
		t.Errorf("outside cmux: %v", err)
	}
	t.Setenv("CMUX_WORKSPACE_ID", "W1")
	err = cmuxMux{}.Prepare("/repo")
	if err == nil || strings.Contains(err.Error(), "allowAll") {
		t.Errorf("inside cmux the hint makes no sense: %v", err)
	}
}

func TestNewMuxPicksCmux(t *testing.T) {
	cfg := defaultConfig()
	t.Setenv("HERDR_ENV", "")
	t.Setenv("CMUX_WORKSPACE_ID", "W1")
	if _, ok := newMux(cfg).(cmuxMux); !ok {
		t.Errorf("auto inside cmux should be cmux, got %T", newMux(cfg))
	}
	t.Setenv("CMUX_WORKSPACE_ID", "")
	cfg.Mux = "cmux"
	if _, ok := newMux(cfg).(cmuxMux); !ok {
		t.Errorf("mux: cmux should be cmux, got %T", newMux(cfg))
	}
	if muxByKind("cmux") == nil {
		t.Error("muxByKind should know cmux")
	}
}

// The whole open/prompt/close flow on cmux: real git worktrees, the
// fake CLI in the multiplexer's seat, no tmux involved.
func TestOpenAndCloseOnCmux(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	fake := newFakeCmux(t)
	f.cfg.Mux = "cmux"
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = `echo "$PR_OWL_MUX|$PR_OWL_SESSION|$PR_OWL_WINDOW" > ` + hookOut

	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	out := f.open("42")
	if !strings.Contains(out, "started "+name+"\n") || strings.Contains(out, "attach with") {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "pr42.txt")) {
		t.Fatalf("worktree %s missing the PR's file", wt)
	}
	w, ok := fake.workspace(name)
	if !ok {
		t.Fatal("no workspace created")
	}
	if got, want := fake.typed(name), []string{"CMUX_SURFACE_ID=SF-1 true '/pr-review:pr-review 42'", "<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	if fake.state().Selected != w.Ref {
		t.Errorf("arriving should have selected the workspace, selected = %q", fake.state().Selected)
	}
	f.waitFile(hookOut, "cmux||"+name+"\n")

	// The agent has exited (the shell alone): a prompt restarts it.
	fake.setTop(name, nil, []string{"zsh"})
	if out := f.open("42", "--prompt", "again"); !strings.Contains(out, "restarted agent in "+name) {
		t.Errorf("output: %q", out)
	}
	fake.setTop(name, []string{"claude"}, []string{"2.1.263", "zsh"})
	if out := f.open("42", "--prompt", "look"); !strings.Contains(out, "sent prompt to "+name) {
		t.Errorf("output: %q", out)
	}
	if got := fake.typed(name); got[len(got)-2] != "look" {
		t.Errorf("typed %v, want the prompt typed last", got)
	}
	fake.addSession(w.ID, "needsInput", true, "2026-09-08T16:00:00Z")
	var buf strings.Builder
	if err := runOpen(f.cfg, []string{"42", "--prompt", "x"}, &buf, true); err == nil || !strings.Contains(err.Error(), "waiting for you in "+name) {
		t.Errorf("prompt while blocked: err = %v", err)
	}

	buf.Reset()
	if err := runClose(f.cfg, []string{"--force", "42"}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, still := fake.workspace(name); !strings.Contains(buf.String(), "closed window "+name) || still || f.exists(wt) {
		t.Errorf("close: %q; workspace gone: %v; worktree gone: %v", buf.String(), !still, !f.exists(wt))
	}
}

// TestCmuxLive runs the adapter against a real cmux when one answers:
// a workspace opened, found, typed into, read, closed. Skipped where
// cmux is not installed or not reachable.
func TestCmuxLive(t *testing.T) {
	if _, err := exec.LookPath("cmux"); err != nil {
		t.Skip("cmux not installed")
	}
	if out, err := exec.Command("cmux", "ping").Output(); err != nil || !strings.Contains(string(out), "PONG") {
		t.Skip("cmux not reachable")
	}
	m := cmuxMux{}
	name := fmt.Sprintf("pr-%d-live-test", os.Getpid())
	dir := t.TempDir()
	if err := m.Open(name, dir, "echo pr-owl-live-test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close(name) })
	if !strings.Contains(strings.Join(m.Windows(), " "), name) {
		t.Errorf("Windows() = %v, want %s", m.Windows(), name)
	}
	if !m.AtShell(name) {
		t.Errorf("a workspace running only its shell should be at a shell")
	}
	if s := m.States()[name]; s != "" {
		t.Errorf("state of a workspace without an agent = %q, want unknown", s)
	}
	if err := m.Close(name); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(m.Windows(), " "), name) {
		t.Errorf("workspace still listed after Close")
	}
}
