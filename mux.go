// Reviews run in a terminal multiplexer: one window per PR among
// whatever else the multiplexer holds, a way to type into a window,
// and what the multiplexer knows about the agent in it. The
// multiplexers themselves — tmux, herdr, cmux — are package mux's;
// this is the review side of them.
package main

import "github.com/stefanahman/mux"

// What the agent in a window is doing, as the multiplexer reports it;
// "" is unknown.
const (
	agentWorking = mux.Working
	agentBlocked = mux.Blocked
	agentDone    = mux.Done
	agentIdle    = mux.Idle
)

// windows is the review windows of a multiplexer: the workspaces named
// pr-<N>[-<slug>], and nothing else the user keeps there.
type windows struct{ d mux.Driver }

// newWindows is the multiplexer for this configuration: the one named,
// or with `mux: auto` herdr or cmux when pr-owl runs inside one of them
// and tmux otherwise.
func newWindows(cfg Config) windows {
	tmux := mux.Tmux{SessionName: cfg.Tmux.Session, Keepalive: cfg.Tmux.KeepaliveWindow}
	herdr := mux.NewHerdr(cfg.Herdr.Socket)
	switch cfg.Mux {
	case "tmux":
		return windows{tmux}
	case "herdr":
		return windows{herdr}
	case "cmux":
		return windows{mux.NewCmux()}
	}
	return windows{mux.Detect(tmux, herdr, mux.NewCmux())}
}

// windowsByKind is the multiplexer a child was told about through
// PR_OWL_MUX, enough to notify with; ok is false for none.
func windowsByKind(kind string) (windows, bool) {
	d := mux.ByKind(kind)
	return windows{d}, d != nil
}

// Kind names the multiplexer: the value of PR_OWL_MUX.
func (w windows) Kind() string { return w.d.Kind() }

// ChildEnv is what a child process needs in its environment to reach
// the same multiplexer.
func (w windows) ChildEnv() []string {
	return append([]string{"PR_OWL_MUX=" + w.d.Kind()}, w.d.ChildEnv()...)
}

// Prepare makes the container of review windows exist.
func (w windows) Prepare(repoDir string) error { return w.d.Prepare(repoDir) }

// list is the review workspaces, by name.
func (w windows) list() ([]mux.Workspace, error) {
	all, err := w.d.Workspaces()
	if err != nil {
		return nil, err
	}
	var reviews []mux.Workspace
	for _, ws := range all {
		if prNumberOf(ws.Name) > 0 {
			reviews = append(reviews, ws)
		}
	}
	return reviews, nil
}

func (w windows) find(name string) (mux.Workspace, error) {
	list, err := w.list()
	if err != nil {
		return mux.Workspace{}, err
	}
	for _, ws := range list {
		if ws.Name == name {
			return ws, nil
		}
	}
	return mux.Workspace{}, &noWindowError{w.d.Kind(), name}
}

type noWindowError struct{ kind, name string }

func (e *noWindowError) Error() string { return e.kind + ": no window " + e.name }

// Windows lists the review windows by name; nil when there are none.
func (w windows) Windows() []string {
	list, err := w.list()
	if err != nil {
		return nil
	}
	var names []string
	for _, ws := range list {
		names = append(names, ws.Name)
	}
	return names
}

// Open creates the window for a worktree and types the agent's start
// line into it.
func (w windows) Open(name, dir, startLine string) error {
	ws, err := w.d.Create(name, dir)
	if err != nil {
		return err
	}
	pane, err := w.d.AgentPane(ws)
	if err != nil {
		return err
	}
	return w.d.Run(ws, pane, startLine)
}

// States reports each review window's agent state, keyed by name.
func (w windows) States() map[string]string {
	states, err := w.d.States()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for name, state := range states {
		if prNumberOf(name) > 0 {
			out[name] = state
		}
	}
	return out
}

// AtShell reports whether the window's agent pane shows a shell, i.e.
// the agent isn't running there.
func (w windows) AtShell(name string) bool {
	ws, err := w.find(name)
	if err != nil {
		return false
	}
	pane, err := w.d.AgentPane(ws)
	return err == nil && w.d.AtShell(ws, pane)
}

// Prompt hands a prompt to the agent in the window.
func (w windows) Prompt(name, text string) error {
	ws, pane, err := w.agentPane(name)
	if err != nil {
		return err
	}
	return w.d.Prompt(ws, pane, text)
}

// Run types a command line into the window's shell.
func (w windows) Run(name, line string) error {
	ws, pane, err := w.agentPane(name)
	if err != nil {
		return err
	}
	return w.d.Run(ws, pane, line)
}

func (w windows) agentPane(name string) (mux.Workspace, mux.Pane, error) {
	ws, err := w.find(name)
	if err != nil {
		return ws, mux.Pane{}, err
	}
	pane, err := w.d.AgentPane(ws)
	return ws, pane, err
}

// Select makes the window the current one: the user is arriving, so
// the multiplexer may count the window as seen.
func (w windows) Select(name string) error {
	ws, err := w.find(name)
	if err != nil {
		return err
	}
	if err := w.d.Select(ws); err != nil {
		return err
	}
	return w.d.Seen(ws)
}

// SwitchClient brings the user's client to the reviews.
func (w windows) SwitchClient() error { return w.d.Focus() }

// Close removes the window.
func (w windows) Close(name string) error {
	ws, err := w.find(name)
	if err != nil {
		return err
	}
	return w.d.Close(ws)
}

// Current is the review window pr-owl was started in, if any.
func (w windows) Current() (string, bool) {
	ws, ok := w.d.Current()
	if !ok || prNumberOf(ws.Name) == 0 {
		return "", false
	}
	return ws.Name, true
}

// Describe names the window the way the user sees it.
func (w windows) Describe(name string) string {
	return w.d.Describe(mux.Workspace{Name: name})
}

// AttachHint tells a user outside the multiplexer how to reach the
// reviews; "" when they are already inside.
func (w windows) AttachHint() string { return w.d.AttachHint() }

// Notify shows a transient message to the user, the multiplexer's way.
func (w windows) Notify(text string) { w.d.Notify("pr-owl", text) }

// Env is what the after_open hook learns about the window.
func (w windows) Env(name string) map[string]string {
	env := map[string]string{"PR_OWL_WINDOW": name}
	if s := w.d.Session(); s != "" {
		env["PR_OWL_SESSION"] = s
	}
	return env
}

// findWindow returns the review window of PR n, or "".
func findWindow(w windows, n int) string {
	for _, name := range w.Windows() {
		if matchesPR(name, n) {
			return name
		}
	}
	return ""
}
