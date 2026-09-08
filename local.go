// Local overlays: git worktrees and review windows that correspond to
// PRs by name convention (`pr-<N>` or `pr-<N>-<slug>`).
//
// Scope: `git worktree list` runs in the working directory, so it lists
// every worktree of the repo owl was launched from. Windows come
// from the multiplexer's review container (one window per PR review);
// window names use the same pr-<N>[-…] pattern as the worktree
// basenames.
package main

import (
	tea "charm.land/bubbletea/v2"
)

// LocalState is the per-PR overlay: does a worktree exist? A review
// window with a Claude conversation? What is the agent in it doing?
type LocalState struct {
	Worktree    string // absolute path to the worktree; "" if none
	Window      string // the review window's name when one exists; "" if none
	ClaudeState string // agentWorking | agentBlocked | agentDone | agentIdle | "" (unknown)
}

// localMsg carries a snapshot of local state keyed by PR handle.
type localMsg map[string]LocalState

// fetchLocal reads worktrees + the multiplexer's review windows and
// merges them into a per-PR map. Failures in either source degrade
// gracefully — you get whatever partial data was available.
func (m model) fetchLocal() tea.Msg {
	worktrees := readWorktrees()
	windows := newWindows(m.cfg, reviews).States()

	out := make(map[string]LocalState)
	for handle, wtPath := range worktrees {
		s := LocalState{Worktree: wtPath}
		if state, ok := windows[handle]; ok {
			s.Window = handle
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
		out[name] = LocalState{Window: name, ClaudeState: state}
	}
	return localMsg(out)
}

// readWorktrees lists the review worktrees of the repo in cwd, keyed
// by handle (directory name `pr-<N>[-<slug>]`, or the branch as a
// fallback — see worktree.handle).
//
// Path-based keying is primary because `owl pr open` names its
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

// findLocalForPR looks up the LocalState of PR n: the entry named
// `pr-<N>` or `pr-<N>-<slug>`.
func findLocalForPR(state map[string]LocalState, prNumber int) LocalState {
	return findLocalBy(state, func(name string) bool { return matchesPR(name, prNumber) })
}

// findLocalBy looks up the LocalState the match names.
func findLocalBy(state map[string]LocalState, match func(name string) bool) LocalState {
	for name, ls := range state {
		if match(name) {
			return ls
		}
	}
	return LocalState{}
}
