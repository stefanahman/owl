package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fakeHerdr is a herdr server good enough for the adapter: the methods
// it calls, over the same newline-JSON socket, with the shapes seen on
// the wire of herdr 0.9.
type fakeHerdr struct {
	t      testing.TB
	socket string

	mu         sync.Mutex
	seq        int
	workspaces []*fakeWorkspace
	focused    string            // workspace id
	statuses   map[string]string // workspace label → agent_status
	foreground map[string]string // pane id → foreground process name
	blocked    map[string]bool   // pane id → agent.prompt refuses
	typed      map[string][]string
	notes      []string
}

type fakeWorkspace struct {
	id, label, cwd, pane string
	focus                bool
}

func newFakeHerdr(t testing.TB) *fakeHerdr {
	t.Helper()
	dir, err := os.MkdirTemp("", "hd") // socket paths are short-lived and length-limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeHerdr{
		t:          t,
		socket:     filepath.Join(dir, "herdr.sock"),
		statuses:   map[string]string{},
		foreground: map[string]string{},
		blocked:    map[string]bool{},
		typed:      map[string][]string{},
	}
	ln, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeHerdr) serve(c net.Conn) {
	defer c.Close()
	rd := bufio.NewReader(c)
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(line, &req) == nil {
				result, herr := f.dispatch(req.Method, req.Params)
				var resp map[string]any
				if herr != nil {
					resp = map[string]any{"id": req.ID, "error": map[string]string{"code": herr.code, "message": herr.message}}
				} else {
					resp = map[string]any{"id": req.ID, "result": result}
				}
				b, _ := json.Marshal(resp)
				_, _ = c.Write(append(b, '\n'))
			}
		}
		if err != nil {
			return
		}
	}
}

func (f *fakeHerdr) find(id string) *fakeWorkspace {
	for _, w := range f.workspaces {
		if w.id == id {
			return w
		}
	}
	return nil
}

func (f *fakeHerdr) paneOf(paneID string) *fakeWorkspace {
	for _, w := range f.workspaces {
		if w.pane == paneID {
			return w
		}
	}
	return nil
}

func (f *fakeHerdr) info(w *fakeWorkspace) map[string]any {
	status := f.statuses[w.label]
	if status == "" {
		status = "unknown"
	}
	return map[string]any{"workspace_id": w.id, "label": w.label, "agent_status": status, "focused": w.id == f.focused, "active_tab_id": w.id + ":t1"}
}

func (f *fakeHerdr) dispatch(method string, raw json.RawMessage) (any, *herdrError) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var p struct {
		Cwd, Label, WorkspaceID, PaneID, Target, Text, Title, Body string
		Focus                                                      bool
		Keys                                                       []string
	}
	_ = json.Unmarshal(raw, &struct {
		Cwd         *string   `json:"cwd"`
		Label       *string   `json:"label"`
		WorkspaceID *string   `json:"workspace_id"`
		PaneID      *string   `json:"pane_id"`
		Target      *string   `json:"target"`
		Text        *string   `json:"text"`
		Title       *string   `json:"title"`
		Body        *string   `json:"body"`
		Focus       *bool     `json:"focus"`
		Keys        *[]string `json:"keys"`
	}{&p.Cwd, &p.Label, &p.WorkspaceID, &p.PaneID, &p.Target, &p.Text, &p.Title, &p.Body, &p.Focus, &p.Keys})
	switch method {
	case "ping":
		return map[string]any{"type": "pong"}, nil
	case "workspace.list":
		list := []map[string]any{}
		for _, w := range f.workspaces {
			list = append(list, f.info(w))
		}
		return map[string]any{"type": "workspace_list", "workspaces": list}, nil
	case "workspace.create":
		f.seq++
		w := &fakeWorkspace{id: fmt.Sprintf("w%d", f.seq), label: p.Label, cwd: p.Cwd, focus: p.Focus}
		w.pane = w.id + ":p1"
		f.workspaces = append(f.workspaces, w)
		if p.Focus || f.focused == "" {
			f.focused = w.id
		}
		return map[string]any{"type": "workspace_created", "workspace": f.info(w), "root_pane": map[string]any{"pane_id": w.pane, "workspace_id": w.id, "agent_status": "unknown"}}, nil
	case "workspace.focus":
		w := f.find(p.WorkspaceID)
		if w == nil {
			return nil, &herdrError{"not_found", "workspace not found"}
		}
		f.focused = w.id
		return map[string]any{"type": "workspace_info", "workspace": f.info(w)}, nil
	case "workspace.close":
		for i, w := range f.workspaces {
			if w.id == p.WorkspaceID {
				f.workspaces = append(f.workspaces[:i], f.workspaces[i+1:]...)
				return map[string]any{"type": "ok"}, nil
			}
		}
		return nil, &herdrError{"not_found", "workspace not found"}
	case "pane.list":
		panes := []map[string]any{}
		for _, w := range f.workspaces {
			if p.WorkspaceID == "" || w.id == p.WorkspaceID {
				panes = append(panes, map[string]any{"pane_id": w.pane, "workspace_id": w.id, "agent_status": f.info(w)["agent_status"]})
			}
		}
		return map[string]any{"type": "pane_list", "panes": panes}, nil
	case "pane.process_info":
		if f.paneOf(p.PaneID) == nil {
			return nil, &herdrError{"not_found", "pane not found"}
		}
		name := f.foreground[p.PaneID]
		if name == "" {
			name = "zsh"
		}
		return map[string]any{"type": "pane_process_info", "process_info": map[string]any{"pane_id": p.PaneID, "foreground_processes": []map[string]any{{"name": name, "pid": 1}}}}, nil
	case "pane.send_input":
		if f.paneOf(p.PaneID) == nil {
			return nil, &herdrError{"not_found", "pane not found"}
		}
		line := p.Text
		for _, k := range p.Keys {
			line += "<" + k + ">"
		}
		f.typed[p.PaneID] = append(f.typed[p.PaneID], line)
		return map[string]any{"type": "ok"}, nil
	case "agent.prompt":
		if f.paneOf(p.Target) == nil {
			return nil, &herdrError{"agent_not_found", "agent target " + p.Target + " not found"}
		}
		if f.blocked[p.Target] {
			return nil, &herdrError{"agent_blocked", "agent " + p.Target + " is blocked and requires interactive input"}
		}
		f.typed[p.Target] = append(f.typed[p.Target], "prompt:"+p.Text)
		return map[string]any{"type": "agent_prompted"}, nil
	case "notification.show":
		f.notes = append(f.notes, p.Title+": "+p.Body)
		return map[string]any{"type": "notification_shown", "shown": true}, nil
	}
	return nil, &herdrError{"method_not_found", "unknown method " + method}
}

func (f *fakeHerdr) workspace(label string) *fakeWorkspace {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, w := range f.workspaces {
		if w.label == label {
			return w
		}
	}
	return nil
}

func (f *fakeHerdr) linesTyped(pane string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed[pane]...)
}

func (f *fakeHerdr) set(m map[string]string, k, v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m[k] = v
}

func herdrForTest(t *testing.T) (herdrMux, *fakeHerdr) {
	t.Helper()
	fake := newFakeHerdr(t)
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_SESSION", "")
	return newHerdrMux(HerdrConfig{Socket: fake.socket}), fake
}

func TestHerdrOpenCreatesAWorkspaceAndTypesTheStartLine(t *testing.T) {
	h, fake := herdrForTest(t)
	if err := h.Prepare("/repo"); err != nil {
		t.Fatal(err)
	}
	if got := h.Windows(); got != nil {
		t.Errorf("Windows before any open = %v", got)
	}
	if err := h.Open("pr-7-fix", "/wt/pr-7-fix", "claude --permission-mode auto '/pr-review:pr-review 7'"); err != nil {
		t.Fatal(err)
	}
	w := fake.workspace("pr-7-fix")
	if w == nil || w.cwd != "/wt/pr-7-fix" || w.focus {
		t.Fatalf("workspace = %+v, want cwd /wt/pr-7-fix and no focus stolen", w)
	}
	if got, want := fake.linesTyped(w.pane), []string{"claude --permission-mode auto '/pr-review:pr-review 7'<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	if got, want := h.Windows(), []string{"pr-7-fix"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Windows = %v, want %v", got, want)
	}
	if got := h.Describe("pr-7-fix"); got != "pr-7-fix" {
		t.Errorf("Describe = %q", got)
	}
}

func TestHerdrStatesAndAtShell(t *testing.T) {
	h, fake := herdrForTest(t)
	for _, name := range []string{"pr-1", "pr-2", "notes"} {
		if err := h.Open(name, "/wt/"+name, "true"); err != nil {
			t.Fatal(err)
		}
	}
	fake.set(fake.statuses, "pr-2", "blocked")
	fake.set(fake.statuses, "notes", "working")
	if got, want := h.States(), map[string]string{"pr-1": "", "pr-2": agentBlocked}; !reflect.DeepEqual(got, want) {
		t.Errorf("States = %v, want %v (unknown is \"\"; notes is not a review)", got, want)
	}
	if !h.AtShell("pr-1") {
		t.Error("a pane with zsh in the foreground should be at a shell")
	}
	fake.set(fake.foreground, fake.workspace("pr-1").pane, "claude")
	if h.AtShell("pr-1") {
		t.Error("a pane running claude is not at a shell")
	}
	if h.AtShell("pr-9") {
		t.Error("an unknown window is not at a shell")
	}
}

func TestHerdrPromptIsRefusedWhileBlocked(t *testing.T) {
	h, fake := herdrForTest(t)
	if err := h.Open("pr-3", "/wt/pr-3", "true"); err != nil {
		t.Fatal(err)
	}
	pane := fake.workspace("pr-3").pane
	if err := h.Prompt("pr-3", "look again"); err != nil {
		t.Fatal(err)
	}
	if err := h.Run("pr-3", "claude -c"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.linesTyped(pane), []string{"true<enter>", "prompt:look again", "claude -c<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	fake.mu.Lock()
	fake.blocked[pane] = true
	fake.mu.Unlock()
	err := h.Prompt("pr-3", "hello")
	var herr *herdrError
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("Prompt while blocked = %v, want herdr's refusal", err)
	}
	if !errors.As(err, &herr) || herr.code != "agent_blocked" {
		t.Errorf("error should carry the code: %#v", err)
	}
}

func TestHerdrSelectCloseCurrentNotify(t *testing.T) {
	h, fake := herdrForTest(t)
	for _, name := range []string{"pr-1", "pr-2"} {
		if err := h.Open(name, "/wt/"+name, "true"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Select("pr-2"); err != nil {
		t.Fatal(err)
	}
	if fake.focused != fake.workspace("pr-2").id {
		t.Errorf("Select should focus the workspace, focused = %q", fake.focused)
	}
	if err := h.SwitchClient(); err != nil {
		t.Errorf("SwitchClient = %v, want nothing to do", err)
	}
	t.Setenv("HERDR_WORKSPACE_ID", fake.workspace("pr-1").id)
	if got, ok := h.Current(); !ok || got != "pr-1" {
		t.Errorf("Current = %q, %v; want pr-1 from HERDR_WORKSPACE_ID", got, ok)
	}
	if err := h.Close("pr-1"); err != nil {
		t.Fatal(err)
	}
	if got, want := h.Windows(), []string{"pr-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Close, Windows = %v, want %v", got, want)
	}
	if err := h.Close("pr-1"); err == nil {
		t.Error("closing a closed window should fail")
	}
	h.Notify("open failed: boom")
	if got, want := fake.notes, []string{"pr-owl: open failed: boom"}; !reflect.DeepEqual(got, want) {
		t.Errorf("notes = %v, want %v", got, want)
	}
	t.Setenv("HERDR_SESSION", "work")
	if got, want := h.Env("pr-2"), map[string]string{"PR_OWL_SESSION": "work", "PR_OWL_WINDOW": "pr-2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Env = %v, want %v", got, want)
	}
}

func TestHerdrAttachHint(t *testing.T) {
	t.Setenv("HERDR_ENV", "")
	if got := (herdrMux{socket: "/x/sessions/work/herdr.sock"}).AttachHint(); got != "herdr --session work" {
		t.Errorf("named session hint = %q", got)
	}
	if got := (herdrMux{socket: "/x/herdr.sock"}).AttachHint(); got != "herdr" {
		t.Errorf("default session hint = %q", got)
	}
	t.Setenv("HERDR_ENV", "1")
	if got := (herdrMux{socket: "/x/herdr.sock"}).AttachHint(); got != "" {
		t.Errorf("inside herdr the hint should be empty, got %q", got)
	}
}

func TestHerdrUnreachableSocketNamesIt(t *testing.T) {
	h := herdrMux{socket: filepath.Join(t.TempDir(), "none.sock")}
	err := h.Prepare("/repo")
	if err == nil || !strings.Contains(err.Error(), "none.sock") {
		t.Errorf("Prepare = %v, want an error naming the socket", err)
	}
	if got := h.Windows(); got != nil {
		t.Errorf("Windows without herdr = %v, want nil", got)
	}
}

func TestNewMuxPicksTheMultiplexer(t *testing.T) {
	cfg := defaultConfig()
	cfg.Herdr.Socket = "/x/herdr.sock"
	t.Setenv("HERDR_ENV", "")
	if _, ok := newMux(cfg).(tmuxMux); !ok {
		t.Errorf("auto outside herdr should be tmux, got %T", newMux(cfg))
	}
	t.Setenv("HERDR_ENV", "1")
	if _, ok := newMux(cfg).(herdrMux); !ok {
		t.Errorf("auto inside herdr should be herdr, got %T", newMux(cfg))
	}
	cfg.Mux = "tmux"
	if _, ok := newMux(cfg).(tmuxMux); !ok {
		t.Errorf("mux: tmux should win over the environment, got %T", newMux(cfg))
	}
	t.Setenv("HERDR_ENV", "")
	cfg.Mux = "herdr"
	if h, ok := newMux(cfg).(herdrMux); !ok || h.socket != "/x/herdr.sock" {
		t.Errorf("mux: herdr should use the configured socket, got %#v", newMux(cfg))
	}
	if muxByKind("tmux") == nil || muxByKind("herdr") == nil || muxByKind("") != nil {
		t.Error("muxByKind should know both kinds and nothing else")
	}
}

// The whole open/prompt/close flow on herdr: real git worktrees, the
// fake server in the multiplexer's seat, no tmux involved.
func TestOpenAndCloseOnHerdr(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	fake := newFakeHerdr(t)
	f.cfg.Mux = "herdr"
	f.cfg.Herdr.Socket = fake.socket
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_SESSION", "work")
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
	w := fake.workspace(name)
	if w == nil || w.cwd != wt || w.focus {
		t.Fatalf("workspace = %+v, want cwd %s, created without focus", w, wt)
	}
	if fake.focused != w.id {
		t.Errorf("arriving should have focused the workspace, focused = %q", fake.focused)
	}
	if got, want := fake.linesTyped(w.pane), []string{"true '/pr-review:pr-review 42'<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	f.waitFile(hookOut, "herdr|work|"+name+"\n")

	// The agent has exited (zsh in the foreground): a prompt restarts it.
	if out := f.open("42", "--prompt", "again"); !strings.Contains(out, "restarted agent in "+name) {
		t.Errorf("output: %q", out)
	}
	fake.set(fake.foreground, w.pane, "claude")
	if out := f.open("42", "--prompt", "look"); !strings.Contains(out, "sent prompt to "+name) {
		t.Errorf("output: %q", out)
	}
	if got := fake.linesTyped(w.pane); got[len(got)-1] != "prompt:look" {
		t.Errorf("last typed %q, want the prompt through agent.prompt", got[len(got)-1])
	}
	fake.set(fake.statuses, name, "blocked")
	var buf strings.Builder
	if err := runOpen(f.cfg, []string{"42", "--prompt", "x"}, &buf, true); err == nil || !strings.Contains(err.Error(), "waiting for you in "+name) {
		t.Errorf("prompt while blocked: err = %v", err)
	}

	buf.Reset()
	if err := runClose(f.cfg, []string{"--force", "42"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "closed window "+name) || fake.workspace(name) != nil || f.exists(wt) {
		t.Errorf("close: %q; workspace gone: %v; worktree gone: %v", buf.String(), fake.workspace(name) == nil, !f.exists(wt))
	}
}
