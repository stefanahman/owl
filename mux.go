// Reviews run in a terminal multiplexer: one window per PR inside
// something that holds them all, a way to type into a window, and what
// the multiplexer knows about the agent in it. tmux and herdr are the
// two; the interface is the exact set of calls pr-owl makes.
package main

import (
	"os"
	"path/filepath"
	"strings"
)

// What the agent in a window is doing, as the multiplexer reports it;
// "" is unknown.
const (
	agentWorking = "working"
	agentBlocked = "blocked"
	agentDone    = "done"
	agentIdle    = "idle"
)

type mux interface {
	// Kind names the multiplexer: the value of PR_OWL_MUX.
	Kind() string
	// ChildEnv is what a child process needs in its environment to
	// reach the same multiplexer: PR_OWL_MUX, and for herdr the socket.
	ChildEnv() []string
	// Prepare makes the container of review windows exist.
	Prepare(repoDir string) error
	// Windows lists the review windows by name, pr-<N>[-<slug>]; nil
	// when there are none.
	Windows() []string
	// Open creates the window for a worktree and types the agent's
	// start line into it.
	Open(name, dir, startLine string) error
	// States reports each window's agent state, keyed by window name.
	States() map[string]string
	// AtShell reports whether the window's foreground process is a
	// shell, i.e. the agent isn't running there.
	AtShell(name string) bool
	// Prompt types a prompt to the agent in the window.
	Prompt(name, text string) error
	// Run types a command line into the window's shell.
	Run(name, line string) error
	// Select makes the window the current one of its container.
	Select(name string) error
	// SwitchClient brings the user's client to the container.
	SwitchClient() error
	// Close removes the window.
	Close(name string) error
	// Current is the review window pr-owl was started in, if any.
	Current() (name string, ok bool)
	// Describe names the window the way the user sees it.
	Describe(name string) string
	// AttachHint tells a user outside the multiplexer how to reach the
	// container; "" when they are already inside.
	AttachHint() string
	// Notify shows a transient message to the user, the multiplexer's
	// way, when there is a user to show it to.
	Notify(text string)
	// Env is what the after_open hook learns about the window.
	Env(name string) map[string]string
}

// newMux is the multiplexer for this configuration: the one named, or
// with `mux: auto` herdr when pr-owl runs inside it and tmux otherwise.
func newMux(cfg Config) mux {
	switch cfg.Mux {
	case "tmux":
		return tmuxMux{cfg.Tmux}
	case "herdr":
		return newHerdrMux(cfg.Herdr)
	}
	if os.Getenv("HERDR_ENV") == "1" {
		return newHerdrMux(cfg.Herdr)
	}
	return tmuxMux{cfg.Tmux}
}

// muxByKind is the multiplexer a child was told about through
// PR_OWL_MUX, enough to notify with; nil for none.
func muxByKind(kind string) mux {
	switch kind {
	case "tmux":
		return tmuxMux{}
	case "herdr":
		return newHerdrMux(HerdrConfig{})
	}
	return nil
}

// isShell reports whether a foreground process name is a shell — the
// agent has exited, and a typed prompt would run as a command.
func isShell(command string) bool {
	switch strings.TrimPrefix(filepath.Base(command), "-") {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "nu":
		return true
	}
	return false
}

// findWindow returns the review window of PR n, or "".
func findWindow(m mux, n int) string {
	for _, w := range m.Windows() {
		if matchesPR(w, n) {
			return w
		}
	}
	return ""
}
