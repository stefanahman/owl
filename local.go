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
	Worktree string // absolute path to the worktree; "" if none
	// Branch is what is checked out there, "" when detached. It is how
	// one of your own PRs finds its workspace: that workspace is named
	// for the issue, so the PR number is nowhere in it.
	Branch      string
	Window      string // the review window's name when one exists; "" if none
	ClaudeState string // agentWorking | agentBlocked | agentDone | agentIdle | "" (unknown)
}

// localMsg carries a snapshot of local state keyed by PR handle.
type localMsg map[string]LocalState

// fetchLocal reads worktrees + the multiplexer's windows of the
// list's scope and merges them into a per-workspace map. Failures in
// either source degrade gracefully — you get whatever partial data was
// available.
func (m model) fetchLocal() tea.Msg {
	return m.fetchLocalIn(m.stateScopes()...)
}

// stateScopes are the containers this list's rows can have windows in.
//
// The PR list shows your own PRs beside the reviews, and one of those
// opens into its branch's workspace — a feature's window, in the other
// container. Reading only the reviews would leave every mine row
// looking unopened while an agent works in it.
func (m model) stateScopes() []scope {
	sc := m.sc
	if sc.owns == nil {
		sc = reviews
	}
	if m.panes() {
		return []scope{sc, features}
	}
	return []scope{sc}
}

// fetchStates reads the agent states alone, for a watch signal to act
// on.
//
// Separate from fetchLocal because under a watch the states cost
// nothing while `git worktree list` costs a process every time, and a
// busy agent signals every few hundred milliseconds — running the git
// command at that rate would be worse than the poll this replaces.
// Worktrees appear when something makes one, which the slow tick is
// soon enough for.
//
// It carries the raw states and merges nothing: a command is a closure
// over the model as it was when the command was made, and merging here
// would fold the new states into an overlay that may since have been
// replaced. withStates does it in Update, against the current one.
func (m model) fetchStates() tea.Msg {
	return statesMsg(statesIn(m.cfg, m.stateDriver, m.stateScopes()...))
}

// withStates is the overlay with the window states replaced and the
// worktrees kept. What a watch signal knows is which windows exist and
// what their agents are doing, never what is checked out.
func withStates(local map[string]LocalState, windows map[string]string) map[string]LocalState {
	out := make(map[string]LocalState, len(local)+len(windows))
	for handle, ls := range local {
		ls.Window, ls.ClaudeState = "", ""
		if state, ok := windows[handle]; ok {
			ls.Window, ls.ClaudeState = handle, state
		}
		// An entry that was only ever a window, whose window has gone,
		// goes with it rather than lingering as an empty row.
		if ls.Worktree == "" && ls.Window == "" {
			continue
		}
		out[handle] = ls
	}
	for name, state := range windows {
		if _, ok := out[name]; !ok {
			out[name] = LocalState{Window: name, ClaudeState: state}
		}
	}
	return out
}

// fetchLocalIn is fetchLocal for the given scopes. Window names are
// disjoint by shape between them, so the merge cannot collide.
func (m model) fetchLocalIn(scopes ...scope) tea.Msg {
	worktrees := readWorktrees()
	windows := statesIn(m.cfg, m.stateDriver, scopes...)

	out := make(map[string]LocalState)
	for handle, wt := range worktrees {
		s := LocalState{Worktree: wt.Path, Branch: wt.Branch}
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
func readWorktrees() map[string]worktree {
	list, err := listWorktrees(".")
	if err != nil {
		return nil
	}
	result := make(map[string]worktree)
	for _, wt := range list {
		if h := wt.handle(); h != "" {
			result[h] = wt
		}
	}
	return result
}

// findLocalBranch looks up the workspace holding a branch. This is how
// one of your own PRs finds its own: the workspace is named for the
// issue the branch carries, so nothing in the name mentions the PR.
func findLocalBranch(state map[string]LocalState, branch string) LocalState {
	if branch == "" {
		return LocalState{}
	}
	for _, ls := range state {
		if ls.Branch == branch {
			return ls
		}
	}
	return LocalState{}
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
