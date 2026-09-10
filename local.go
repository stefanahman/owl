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
	sc := m.sc
	if sc.owns == nil {
		sc = reviews
	}
	// The PR list shows your own PRs beside the reviews, and one of
	// those opens into its branch's workspace — a feature's window, in
	// the other container. Reading only the reviews would leave every
	// mine row looking unopened while an agent works in it.
	if m.panes() {
		return m.fetchLocalIn(sc, features)
	}
	return m.fetchLocalIn(sc)
}

// fetchLocalIn is fetchLocal for the given scopes. Window names are
// disjoint by shape between them, so the merge cannot collide.
func (m model) fetchLocalIn(scopes ...scope) tea.Msg {
	worktrees := readWorktrees()
	windows := statesIn(m.cfg, scopes...)

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
