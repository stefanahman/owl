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
	"strings"

	tea "charm.land/bubbletea/v2"
)

// claudeStateOption is the tmux window option tmux-claude-status
// writes Claude's state to — the contract between the two tools.
const claudeStateOption = "@claude-state"

// LocalState is the per-PR overlay: does a worktree exist? A tmux
// window (in the review session) with a Claude conversation? What
// state did the last Claude hook write?
type LocalState struct {
	Worktree    string // absolute path to the worktree; "" if none
	Session     string // the review window's name when one exists (a workspace is a window of tmux.session); "" if none
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
// checked out detached, branch-only matching drops them entirely — a
// review vanished from the overlay that way once.
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
// value of the state option into a map. tmux-claude-status writes that
// window option from Claude Code's hooks: working / blocked / done /
// idle. Absent value → empty string (fresh window).
//
// Only windows of the review session — other tmux sessions aren't PR
// reviews and don't belong in this overlay.
func readReviewWindows(t TmuxConfig) map[string]string {
	format := "#{window_name}\t#{" + claudeStateOption + "}"
	out, err := tmux("list-windows", "-t", tmuxTarget(t.Session, ""), "-F", format)
	if err != nil {
		return nil // review session doesn't exist yet — treat as empty
	}
	result := make(map[string]string)
	for _, line := range strings.Split(out, "\n") {
		name, state, _ := strings.Cut(line, "\t")
		// Skip the keepalive window — no state to report.
		if name == "" || name == t.KeepaliveWindow {
			continue
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
