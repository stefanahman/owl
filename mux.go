// Workspaces run in a terminal multiplexer: one window per PR review
// or per issue among whatever else the multiplexer holds, a way to
// type into a window, and what the multiplexer knows about the agent
// in it. The multiplexers themselves — tmux, herdr, cmux — are package
// mux's; this is owl's side of them.
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

// scope is one kind of workspace owl keeps in a multiplexer: which
// window names are its, and under tmux which session holds them. The
// reviews are one scope; the features another.
type scope struct {
	name    string                   // for messages: reviews, features
	session func(cfg Config) string  // the tmux session
	owns    func(window string) bool // whether a window name is one of ours
}

// reviews is the scope of PR reviews: windows named pr-<N>[-<slug>].
var reviews = scope{
	name:    "reviews",
	session: func(cfg Config) string { return cfg.Tmux.Session },
	owns:    func(w string) bool { return prNumberOf(w) > 0 },
}

// features is the scope of issues being worked on: windows named
// after the branch Linear names, <team>-<n>-<slug>, in the tmux
// session issue.session.
var features = scope{
	name:    "features",
	session: func(cfg Config) string { return cfg.Issue.Session },
	owns:    func(w string) bool { return issueKeyOf(w) != "" },
}

// projects is the scope of the conversations above the issues: windows
// named proj-<slug>, in the tmux session project.session.
var projects = scope{
	name:    "projects",
	session: func(cfg Config) string { return cfg.Project.Session },
	owns:    func(w string) bool { return projectSlugOf(w) != "" },
}

// scopeOf is the scope of a list kind: features for issue, projects
// for project, reviews for pr.
func scopeOf(kind string) scope {
	switch kind {
	case "issue":
		return features
	case "project":
		return projects
	}
	return reviews
}

// scopeForName is the scope a workspace belongs to, read from its own
// name.
//
// The container has to follow the workspace rather than the command
// that opened it. `owl pr open` on a PR of yours resolves to its
// branch's workspace, which is a feature's when the branch carries an
// issue key; a window for it among the reviews would be a second
// window — with a second agent — on the one worktree the issue list is
// already using.
func scopeForName(name string) scope {
	switch {
	case projectSlugOf(name) != "":
		return projects
	case issueKeyOf(name) != "":
		return features
	}
	return reviews
}

// windows is one scope's windows in a multiplexer, and nothing else
// the user keeps there.
type windows struct {
	d  mux.Driver
	sc scope
	// style is how the scope's group is drawn where the multiplexer
	// draws one, and nil when grouping is off. It rides here rather
	// than through Open's arguments: newWindows already has the config,
	// and Open's three strings are the whole of what a caller needs to
	// know.
	style *mux.GroupStyle
}

// groupStyle is how the scope's group is drawn, or nil when grouping
// is off — one value carrying both facts, so there is no way to hold a
// style for a group that is never made. The conversion lives here
// rather than in config.go: the config holds strings, and this is the
// file that knows the multiplexer.
func groupStyle(cfg Config, sc scope) *mux.GroupStyle {
	if !cfg.Groups.Enabled {
		return nil
	}
	s := cfg.Groups.Reviews
	switch sc.name {
	case "features":
		s = cfg.Groups.Features
	case "projects":
		s = cfg.Groups.Projects
	}
	return &mux.GroupStyle{Color: s.Color, Icon: s.Icon}
}

// newWindows is the multiplexer for this configuration, seen through
// one scope: the multiplexer named, or with `mux: auto` herdr or cmux
// when owl runs inside one of them and tmux otherwise.
func newWindows(cfg Config, sc scope) windows {
	tmux := mux.Tmux{SessionName: sc.session(cfg), Keepalive: cfg.Tmux.KeepaliveWindow}
	herdr := mux.NewHerdr(cfg.Herdr.Socket)
	style := groupStyle(cfg, sc)
	switch cfg.Mux {
	case "tmux":
		return windows{tmux, sc, style}
	case "herdr":
		return windows{herdr, sc, style}
	case "cmux":
		return windows{mux.NewCmux(), sc, style}
	}
	return windows{mux.Detect(tmux, herdr, mux.NewCmux()), sc, style}
}

// windowsByKind is the multiplexer a child was told about through
// OWL_MUX, enough to notify with; ok is false for none.
func windowsByKind(kind string) (windows, bool) {
	d := mux.ByKind(kind)
	return windows{d, reviews, nil}, d != nil
}

// Kind names the multiplexer: the value of OWL_MUX.
func (w windows) Kind() string { return w.d.Kind() }

// ChildEnv is what a child process needs in its environment to reach
// the same multiplexer.
func (w windows) ChildEnv() []string {
	return append([]string{"OWL_MUX=" + w.d.Kind()}, w.d.ChildEnv()...)
}

// Ping checks the multiplexer answers and was not started from inside
// a Claude Code session (or, for cmux, from a shell inside tmux):
// every agent in such a one runs without transcript or without the
// hooks the states come from, and the error names the fix.
func (w windows) Ping() error { return w.d.Ping() }

// Prepare makes the container of the scope's windows exist.
func (w windows) Prepare(repoDir string) error { return w.d.Prepare(repoDir) }

// list is the scope's workspaces, by name.
func (w windows) list() ([]mux.Workspace, error) {
	all, err := w.d.Workspaces()
	if err != nil {
		return nil, err
	}
	var ours []mux.Workspace
	for _, ws := range all {
		if w.sc.owns(ws.Name) {
			ours = append(ours, ws)
		}
	}
	return ours, nil
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

// Windows lists the scope's windows by name; nil when there are none.
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
	// Here and nowhere else. This is the one path that creates a
	// workspace — every other branch of workspace.open finds the window
	// already there — and grouping on each open would drag back one the
	// user had pulled out of its group by hand. Nothing on the close
	// path either: cmux drops a group when its last member goes.
	//
	// A group is known by its name, since the id mux carries is not
	// reachable from cmux's CLI. Rename the group in the sidebar and
	// owl stops finding it; the next open makes a second one under the
	// old name.
	//
	// The error is dropped on purpose. Open's job is a workspace with
	// the agent running in it; a sidebar colour that did not take is
	// not worth failing that, and there is nowhere here to report it.
	if w.style != nil {
		_ = mux.Group(w.d, w.sc.name, ws, *w.style)
	}
	pane, err := w.d.AgentPane(ws)
	if err != nil {
		return err
	}
	return w.d.Run(ws, pane, startLine)
}

// States reports each of the scope's windows' agent state, keyed by
// name.
func (w windows) States() map[string]string {
	states, err := w.d.States()
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for name, state := range states {
		if w.sc.owns(name) {
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

// SwitchClient brings the user's client to the scope's windows.
func (w windows) SwitchClient() error { return w.d.Focus() }

// Close removes the window.
func (w windows) Close(name string) error {
	ws, err := w.find(name)
	if err != nil {
		return err
	}
	return w.d.Close(ws)
}

// Current is the scope's window owl was started in, if any.
func (w windows) Current() (string, bool) {
	ws, ok := w.d.Current()
	if !ok || !w.sc.owns(ws.Name) {
		return "", false
	}
	return ws.Name, true
}

// Describe names the window the way the user sees it.
func (w windows) Describe(name string) string {
	return w.d.Describe(mux.Workspace{Name: name})
}

// AttachHint tells a user outside the multiplexer how to reach the
// scope's windows; "" when they are already inside.
func (w windows) AttachHint() string { return w.d.AttachHint() }

// Notify shows a transient message to the user, the multiplexer's way.
func (w windows) Notify(text string) { w.d.Notify("owl", text) }

// Env is what the after_open hook learns about the window.
func (w windows) Env(name string) map[string]string {
	env := map[string]string{"OWL_WINDOW": name}
	if s := w.d.Session(); s != "" {
		env["OWL_SESSION"] = s
	}
	return env
}

// findWindow returns the first window the match names, or "".
func findWindow(w windows, match func(name string) bool) string {
	for _, name := range w.Windows() {
		if match(name) {
			return name
		}
	}
	return ""
}
