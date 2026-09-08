// tmux as the multiplexer: the review session with its keepalive
// window, one window per PR, keystrokes through send-keys. What the
// agent is doing comes from tmux-claude-status, which writes Claude
// Code's hook events to the @claude-state window option; without the
// plugin every state is unknown, and open, close and prompts still
// work.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// claudeStateOption is the tmux window option tmux-claude-status
// writes Claude's state to — the contract between the two tools.
const claudeStateOption = "@claude-state"

type tmuxMux struct{ cfg TmuxConfig }

// tmux runs a tmux command and returns trimmed stdout.
func tmux(args ...string) (string, error) {
	return runOut(exec.Command("tmux", args...))
}

// tmuxTarget builds an exact-match `-t` argument. Without the `=`
// prefix tmux falls back to prefix matching, so `pr-1` would resolve
// to `pr-12-foo` when `pr-1` itself doesn't exist.
func tmuxTarget(session, window string) string {
	if window == "" {
		return "=" + session
	}
	return "=" + session + ":=" + window
}

func (t tmuxMux) target(window string) string { return tmuxTarget(t.cfg.Session, window) }

func (tmuxMux) Kind() string { return "tmux" }

// Prepare creates the review session with its keepalive window when
// it doesn't exist.
func (t tmuxMux) Prepare(dir string) error {
	if _, err := tmux("has-session", "-t", t.target("")); err == nil {
		return nil
	}
	if _, err := tmux("new-session", "-d", "-s", t.cfg.Session, "-n", t.cfg.KeepaliveWindow, "-c", dir); err != nil {
		// Two children starting at once (s on two PRs) both saw no
		// session; the loser of the race finds the winner's.
		if _, again := tmux("has-session", "-t", t.target("")); again == nil {
			return nil
		}
		return err
	}
	return nil
}

func (t tmuxMux) Windows() []string {
	out, err := tmux("list-windows", "-t", t.target(""), "-F", "#{window_name}")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

func (t tmuxMux) Open(name, dir, startLine string) error {
	if _, err := tmux("new-window", "-d", "-t", t.target(""), "-c", dir, "-n", name); err != nil {
		return err
	}
	// Freeze the name — otherwise tmux renames the window after the
	// agent process, and the window stops matching the worktree.
	if _, err := tmux("set-option", "-w", "-t", t.target(name), "automatic-rename", "off"); err != nil {
		return err
	}
	return t.typeLine(name, startLine)
}

// States reads every window of the review session with the value of
// the state option. Absent value → "" (a fresh window, or no plugin).
// The keepalive window has no state to report.
func (t tmuxMux) States() map[string]string {
	out, err := tmux("list-windows", "-t", t.target(""), "-F", "#{window_name}\t#{"+claudeStateOption+"}")
	if err != nil {
		return nil // no review session yet
	}
	states := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		name, state, _ := strings.Cut(line, "\t")
		if name == "" || name == t.cfg.KeepaliveWindow {
			continue
		}
		states[name] = state
	}
	return states
}

func (t tmuxMux) AtShell(name string) bool {
	out, err := tmux("display-message", "-p", "-t", t.target(name), "#{pane_current_command}")
	if err != nil {
		return false
	}
	switch strings.TrimPrefix(filepath.Base(out), "-") {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "nu":
		return true
	}
	return false
}

func (t tmuxMux) Prompt(name, text string) error { return t.typeLine(name, text) }

func (t tmuxMux) Run(name, line string) error { return t.typeLine(name, line) }

// typeLine types text into the window as literal keystrokes, then Enter.
func (t tmuxMux) typeLine(name, text string) error {
	if _, err := tmux("send-keys", "-t", t.target(name), "-l", text); err != nil {
		return err
	}
	_, err := tmux("send-keys", "-t", t.target(name), "Enter")
	return err
}

func (t tmuxMux) Select(name string) error {
	_, err := tmux("select-window", "-t", t.target(name))
	return err
}

func (t tmuxMux) SwitchClient() error {
	_, err := tmux("switch-client", "-t", t.target(""))
	return err
}

func (t tmuxMux) Close(name string) error {
	_, err := tmux("kill-window", "-t", t.target(name))
	return err
}

func (tmuxMux) Current() (string, bool) {
	if os.Getenv("TMUX") == "" {
		return "", false
	}
	w, err := tmux("display-message", "-p", "#{window_name}")
	return w, err == nil && w != ""
}

func (t tmuxMux) Describe(name string) string { return t.cfg.Session + ":" + name }

func (t tmuxMux) AttachHint() string {
	if os.Getenv("TMUX") != "" {
		return ""
	}
	return "tmux attach -t " + t.cfg.Session
}

// Notify puts the text on the status line of the client this process
// belongs to, for eight seconds.
func (tmuxMux) Notify(text string) {
	if os.Getenv("TMUX") == "" {
		return
	}
	_, _ = tmux("display-message", "-d", "8000", text)
}

func (t tmuxMux) Env(name string) map[string]string {
	return map[string]string{"PR_OWL_SESSION": t.cfg.Session, "PR_OWL_WINDOW": name}
}
