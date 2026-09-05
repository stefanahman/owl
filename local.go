// Local overlays: git worktrees and tmux windows that correspond to
// PRs by name convention (`pr-<N>` or `pr-<N>-<slug>`).
//
// Scope: `git worktree list` runs in the working directory, so it lists
// every worktree of the repo pr-owl was launched from. tmux windows
// come from the review session (`tmux.session`, one window per PR
// review); window names use the same pr-<N>[-…] pattern as the
// worktree basenames.
package main

import (
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LocalState is the per-PR overlay: does a worktree exist? A tmux
// window (in the review session) with a Claude conversation? What
// state did the last Claude hook write?
//
// `Session` is retained as the field name for backwards-compat with
// existing pr-owl consumers (badges, guards for `f` / `c`); its value
// under the consolidated model is the tmux window NAME within the
// review session — semantically "does a review workspace exist for
// this PR", just physically now a window instead of a session.
type LocalState struct {
	Worktree    string // absolute path to the worktree; "" if none
	Session     string // <tmux.session>:<window> exists → window name; "" if none
	ClaudeState string // "working" | "blocked" | "done" | "idle" | "" (absent)
}

// localMsg carries a snapshot of local state keyed by PR handle.
type localMsg map[string]LocalState

// fetchLocal reads worktrees + tmux windows of the review session and
// merges them into a per-PR map. Failures in either source degrade
// gracefully — you get whatever partial data was available.
func (m model) fetchLocal() tea.Msg {
	worktrees := readWorktrees()
	windows := readReviewWindows(m.cfg.Tmux)

	out := make(map[string]LocalState)
	for handle, wtPath := range worktrees {
		s := LocalState{Worktree: wtPath}
		if state, ok := windows[handle]; ok {
			s.Session = handle
			s.ClaudeState = state
		}
		out[handle] = s
	}
	// Window with no worktree is unusual but possible during transitions;
	// surface it too so we don't drop the Claude-state signal.
	for name, state := range windows {
		if _, ok := out[name]; ok {
			continue
		}
		out[name] = LocalState{Session: name, ClaudeState: state}
	}
	return localMsg(out)
}

// readWorktrees lists the review worktrees of the repo in cwd, keyed
// by handle (directory name `pr-<N>[-<slug>]`, or the branch as a
// fallback — see worktree.handle).
//
// Path-based keying is primary because `pr-owl open` names its
// worktrees `<repo>/<worktrees_dir>/pr-<N>-<slug>` and they often end
// up in detached HEAD (no `branch refs/heads/…` line to parse). Once
// checked out detached, branch-only matching drops them entirely — the
// exact bug that hid PR 4115.
//
// Failure returns nil — the TUI still renders without wt badges.
func readWorktrees() map[string]string {
	list, err := listWorktrees(".")
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	for _, wt := range list {
		if h := wt.handle(); h != "" {
			result[h] = wt.Path
		}
	}
	return result
}

// readReviewWindows lists the windows of the review session with the
// value of the state option (`#{@claude-state}` by default) into a
// map. tmux-claude-status writes that window option from Claude Code's
// hooks: working / blocked / done / idle. Absent value → empty string
// (fresh window).
//
// Only windows of the review session — other tmux sessions aren't PR
// reviews and don't belong in this overlay.
func readReviewWindows(t TmuxConfig) map[string]string {
	format := "#{window_name}\t#{" + t.StateOption + "}"
	out, err := exec.Command("tmux", "list-windows", "-t", tmuxTarget(t.Session, ""), "-F", format).Output()
	if err != nil {
		return nil // review session doesn't exist yet — treat as empty
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		// Skip the keepalive window — no state to report.
		if name == t.KeepaliveWindow {
			continue
		}
		state := ""
		if len(parts) == 2 {
			state = parts[1]
		}
		result[name] = state
	}
	return result
}

// findLocalForPR looks up a LocalState for the given PR number by
// matching names of the form `pr-<N>` or `pr-<N>-<slug>`.
func findLocalForPR(state map[string]LocalState, prNumber int) LocalState {
	for name, ls := range state {
		if matchesPR(name, prNumber) {
			return ls
		}
	}
	return LocalState{}
}
