// cmux as the multiplexer: one cmux workspace per PR, its terminal
// running the agent. Everything goes through the `cmux` CLI cmux
// ships, which finds the app's socket itself; the shapes below are the
// ones cmux 0.64.22 prints. State comes from cmux's own Claude Code
// hooks, which it injects through a wrapper when `claude` starts in
// one of its terminals.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type cmuxMux struct{}

// cmuxRun runs one cmux command and returns trimmed stdout. Errors
// carry cmux's stderr, which is where the message is. CMUX_QUIET
// silences the notices cmux prints for its older verb names.
func (cmuxMux) run(args ...string) (string, error) {
	cmd := exec.Command("cmux", args...)
	cmd.Env = append(os.Environ(), "CMUX_QUIET=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("cmux %s: %s", args[0], msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// runJSON runs a command with --json and decodes its answer into v.
func (c cmuxMux) runJSON(v any, args ...string) error {
	out, err := c.run(append([]string{"--json", "--id-format", "both"}, args...)...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		return fmt.Errorf("cmux %s: %w", args[0], err)
	}
	return nil
}

func (cmuxMux) Kind() string { return "cmux" }

func (cmuxMux) ChildEnv() []string { return []string{"PR_OWL_MUX=cmux"} }

// insideCmux reports whether this process runs in a cmux terminal:
// cmux names the workspace in the environment of every one.
func insideCmux() bool { return os.Getenv("CMUX_WORKSPACE_ID") != "" }

// Prepare has nothing to create — reviews are workspaces, and cmux has
// no container above them — but it makes sure cmux answers. Its socket
// admits only processes started inside cmux unless told otherwise.
func (c cmuxMux) Prepare(string) error {
	if _, err := c.run("ping"); err != nil {
		if insideCmux() {
			return err
		}
		return fmt.Errorf("%w (run pr-owl in a cmux terminal, or start cmux with CMUX_SOCKET_MODE=allowAll)", err)
	}
	return nil
}

type cmuxWorkspace struct {
	ID          string `json:"id"`
	Ref         string `json:"ref"`
	Title       string `json:"title"`
	CustomTitle string `json:"custom_title"`
	HasCustom   bool   `json:"has_custom_title"`
	Selected    bool   `json:"selected"`
}

// label is what the sidebar shows: the name given at creation, else
// what cmux derived from the directory or the command.
func (w cmuxWorkspace) label() string {
	if w.HasCustom {
		return w.CustomTitle
	}
	return w.Title
}

func (c cmuxMux) workspaces() ([]cmuxWorkspace, error) {
	var r struct {
		Workspaces []cmuxWorkspace `json:"workspaces"`
	}
	if err := c.runJSON(&r, "workspace", "list"); err != nil {
		return nil, err
	}
	return r.Workspaces, nil
}

// Windows are the workspaces named like reviews; cmux holds the user's
// other work too.
func (c cmuxMux) Windows() []string {
	ws, err := c.workspaces()
	if err != nil {
		return nil
	}
	var names []string
	for _, w := range ws {
		if prNumberOf(w.label()) > 0 {
			names = append(names, w.label())
		}
	}
	return names
}

func (c cmuxMux) workspace(name string) (cmuxWorkspace, error) {
	ws, err := c.workspaces()
	if err != nil {
		return cmuxWorkspace{}, err
	}
	for _, w := range ws {
		if w.label() == name {
			return w, nil
		}
	}
	return cmuxWorkspace{}, fmt.Errorf("cmux: no workspace %q", name)
}

type cmuxSurface struct {
	ID  string `json:"id"`
	Ref string `json:"ref"`
}

// surface is the workspace's terminal: the first surface of its first
// pane, the one it was created with. A review never splits it.
func (c cmuxMux) surface(ws cmuxWorkspace) (cmuxSurface, error) {
	var r struct {
		Surfaces []cmuxSurface `json:"surfaces"`
	}
	if err := c.runJSON(&r, "list-pane-surfaces", "--workspace", ws.Ref); err != nil {
		return cmuxSurface{}, err
	}
	if len(r.Surfaces) == 0 {
		return cmuxSurface{}, fmt.Errorf("cmux: workspace %q has no surface", ws.label())
	}
	return r.Surfaces[0], nil
}

var cmuxCreated = regexp.MustCompile(`\bworkspace:\d+\b`)

// Open creates the workspace without taking the focus — Select does
// that when the user is arriving — and types the start line into its
// shell. The shell, not `--command`: cmux hooks Claude Code through a
// wrapper on the shell's PATH, and a command given at creation runs
// without it.
func (c cmuxMux) Open(name, dir, startLine string) error {
	out, err := c.run("workspace", "create", "--name", name, "--cwd", dir, "--focus", "false")
	if err != nil {
		return err
	}
	ref := cmuxCreated.FindString(out)
	if ref == "" {
		return fmt.Errorf("cmux workspace create: no workspace in the answer %q", out)
	}
	sf, err := c.surface(cmuxWorkspace{Ref: ref, CustomTitle: name, HasCustom: true})
	if err != nil {
		return err
	}
	return c.typeLine(sf, c.hooked(sf, startLine))
}

// hooked gives the wrapper what it needs. cmux 0.64.22 puts
// CMUX_WORKSPACE_ID and CMUX_TAB_ID in a terminal's environment but not
// CMUX_SURFACE_ID, the one variable its Claude Code wrapper checks
// before injecting its hooks; without them the state stays unknown. A
// cmux that gives pr-owl's own terminal the variable gives every
// terminal the variable, and the line is typed as it is.
func (cmuxMux) hooked(sf cmuxSurface, line string) string {
	if os.Getenv("CMUX_SURFACE_ID") != "" || sf.ID == "" {
		return line
	}
	return "CMUX_SURFACE_ID=" + sf.ID + " " + line
}

// typeLine types a line and Enter as one submission.
func (c cmuxMux) typeLine(sf cmuxSurface, line string) error {
	if _, err := c.run("send", "--surface", sf.Ref, line); err != nil {
		return err
	}
	_, err := c.run("send-key", "--surface", sf.Ref, "enter")
	return err
}

// cmuxSession is a record of cmux's Claude Code hook store: one per
// `claude` its wrapper started, kept after the agent exits.
type cmuxSession struct {
	Workspace string `json:"workspace_id"`
	Lifecycle string `json:"agent_lifecycle"` // running, idle, needsInput, unknown
	PIDExists bool   `json:"stored_pid_exists"`
	UpdatedAt string `json:"updated_at"`
}

func (c cmuxMux) sessions() ([]cmuxSession, error) {
	var r struct {
		Sessions []cmuxSession `json:"sessions"`
	}
	if err := c.runJSON(&r, "sessions", "--agent", "claude"); err != nil {
		return nil, err
	}
	return r.Sessions, nil
}

// unread lists the workspaces with a notification not yet read: cmux
// posts one when Claude finishes or needs permission.
func (c cmuxMux) unread() map[string]bool {
	var notes []struct {
		Workspace string `json:"workspace_id"`
		Read      bool   `json:"is_read"`
	}
	if err := c.runJSON(&notes, "list-notifications"); err != nil {
		return nil
	}
	m := map[string]bool{}
	for _, n := range notes {
		if !n.Read {
			m[n.Workspace] = true
		}
	}
	return m
}

// States maps cmux's words onto pr-owl's, from the hook store's live
// records: running is working, needsInput is blocked, idle is done
// while cmux's notification about the finished turn is unread and idle
// once it has been read. A workspace without a live record — no agent,
// or one the wrapper never saw — is "".
func (c cmuxMux) States() map[string]string {
	ws, err := c.workspaces()
	if err != nil {
		return nil
	}
	live := map[string]cmuxSession{}
	if sessions, err := c.sessions(); err == nil {
		for _, s := range sessions {
			if s.PIDExists && s.UpdatedAt >= live[s.Workspace].UpdatedAt {
				live[s.Workspace] = s
			}
		}
	}
	var unread map[string]bool
	states := map[string]string{}
	for _, w := range ws {
		if prNumberOf(w.label()) == 0 {
			continue
		}
		states[w.label()] = ""
		s, ok := live[w.ID]
		if !ok {
			continue
		}
		switch s.Lifecycle {
		case "running":
			states[w.label()] = agentWorking
		case "needsInput":
			states[w.label()] = agentBlocked
		case "idle":
			if unread == nil {
				unread = c.unread()
			}
			if unread[w.ID] {
				states[w.label()] = agentDone
			} else {
				states[w.label()] = agentIdle
			}
		}
	}
	return states
}

// AtShell asks cmux what runs in the workspace: a coding agent it has
// detected by process, or the processes attributed to its terminal.
// The shell alone, or nothing, means the agent has exited.
func (c cmuxMux) AtShell(name string) bool {
	w, err := c.workspace(name)
	if err != nil {
		return false
	}
	var top struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"coding_agents"`
	}
	if err := c.runJSON(&top, "top", "--workspace", w.Ref, "--processes"); err != nil || len(top.Agents) > 0 {
		return false
	}
	// The process rows, tab-separated: cpu, memory, count, kind, ref,
	// parent, name. Only what top attributes to the terminal is listed,
	// and an idle shell may not be.
	out, err := c.run("top", "--workspace", w.Ref, "--processes", "--format", "tsv")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) >= 7 && f[3] == "process" && !isShell(f[6]) {
			return false
		}
	}
	return true
}

// Prompt types the text to the agent, as tmux would: cmux has no
// prompt call of its own for a terminal agent. The caller refuses
// while the state is blocked, so keystrokes never answer a dialog.
func (c cmuxMux) Prompt(name, text string) error {
	w, err := c.workspace(name)
	if err != nil {
		return err
	}
	sf, err := c.surface(w)
	if err != nil {
		return err
	}
	return c.typeLine(sf, text)
}

func (c cmuxMux) Run(name, line string) error {
	w, err := c.workspace(name)
	if err != nil {
		return err
	}
	sf, err := c.surface(w)
	if err != nil {
		return err
	}
	return c.typeLine(sf, c.hooked(sf, line))
}

// Select shows the workspace in its window and, the user arriving,
// marks its notifications read: done becomes idle.
func (c cmuxMux) Select(name string) error {
	w, err := c.workspace(name)
	if err != nil {
		return err
	}
	if _, err := c.run("workspace", "select", "--workspace", w.Ref); err != nil {
		return err
	}
	_, _ = c.run("mark-notification-read", "--workspace", w.Ref)
	return nil
}

// SwitchClient brings cmux's window to the front, for a pr-owl running
// outside it; inside, the window is already there.
func (c cmuxMux) SwitchClient() error {
	if insideCmux() {
		return nil
	}
	var r struct {
		Window string `json:"window_ref"`
	}
	if err := c.runJSON(&r, "workspace", "list"); err != nil {
		return err
	}
	_, err := c.run("focus-window", "--window", r.Window)
	return err
}

func (c cmuxMux) Close(name string) error {
	w, err := c.workspace(name)
	if err != nil {
		return err
	}
	_, err = c.run("workspace", "close", "--workspace", w.Ref)
	return err
}

// Current is the workspace of the terminal pr-owl runs in, which cmux
// names in the environment.
func (c cmuxMux) Current() (string, bool) {
	id := os.Getenv("CMUX_WORKSPACE_ID")
	if id == "" {
		return "", false
	}
	ws, err := c.workspaces()
	if err != nil {
		return "", false
	}
	for _, w := range ws {
		if w.ID == id {
			return w.label(), true
		}
	}
	return "", false
}

func (cmuxMux) Describe(name string) string { return name }

func (cmuxMux) AttachHint() string {
	if insideCmux() {
		return ""
	}
	return "cmux"
}

func (c cmuxMux) Notify(text string) {
	args := []string{"notify", "--title", "pr-owl", "--body", text}
	if id := os.Getenv("CMUX_WORKSPACE_ID"); id != "" {
		args = append(args, "--workspace", id)
	}
	_, _ = c.run(args...)
}

func (cmuxMux) Env(name string) map[string]string {
	return map[string]string{"PR_OWL_WINDOW": name}
}
