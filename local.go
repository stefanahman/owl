// Local overlays: git worktrees and tmux windows that correspond to
// PRs by name convention (`pr-<N>` or `pr-<N>-<slug>`).
//
// Scope: `git worktree list` runs in the working directory, so it lists
// every worktree of the repo pr-owl was launched from. tmux windows
// come from the consolidated `pr-reviews` session (one window per PR
// review, created by the pr-review script); window names use the same
// pr-<N>[-…] pattern as the worktree basenames.
package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// LocalState is the per-PR overlay: does a worktree exist? A tmux
// window (in the `pr-reviews` session) with a Claude conversation?
// What state did the last Claude hook write?
//
// `Session` is retained as the field name for backwards-compat with
// existing pr-owl consumers (badges, guards for `f` / `c`); its value
// under the consolidated model is the tmux window NAME within the
// pr-reviews session — semantically "does a review workspace exist
// for this PR", just physically now a window instead of a session.
type LocalState struct {
	Worktree    string // absolute path to the worktree; "" if none
	Session     string // pr-reviews:<window> exists → window name; "" if none
	ClaudeState string // "amber" | "working" | "green" | "read" | "" (absent)
}

// localMsg carries a snapshot of local state keyed by PR handle.
type localMsg map[string]LocalState

// reviewsSessionName is the fixed tmux session that holds all per-PR
// review windows (see pr-review script). Kept as a constant so
// changing it here + in the shell scripts stays in sync.
const reviewsSessionName = "pr-reviews"

// fetchLocal reads worktrees + tmux windows of the pr-reviews session
// and merges them into a per-PR map. Failures in either source
// degrade gracefully — you get whatever partial data was available.
func fetchLocal() tea.Msg {
	worktrees := readWorktrees()
	windows := readReviewWindows()

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

// readWorktrees parses `git worktree list --porcelain` (run in cwd) and
// returns a map keyed by the PR-handle derived from the worktree path
// (basename matching `pr-<N>[-<slug>]`) plus, as fallback, the branch
// name for any worktree whose path doesn't fit the convention.
//
// Path-based keying is primary because the pr-review script names its
// worktrees `<repo>/.worktrees.local/pr-<N>-<slug>` and often leaves
// them in detached HEAD (no `branch refs/heads/…` line to parse). Once
// checked out detached, branch-only matching drops them entirely — the
// exact bug that hid PR 4115.
//
// Failure returns nil — the TUI still renders without wt badges.
func readWorktrees() map[string]string {
	out, err := exec.Command("git", "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	result := make(map[string]string)
	var currentWT, currentBranch string
	flush := func() {
		if currentWT == "" {
			return
		}
		base := filepath.Base(currentWT)
		if prHandleRe.MatchString(base) {
			result[base] = currentWT
		} else if currentBranch != "" {
			result[currentBranch] = currentWT
		}
	}
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			currentWT = strings.TrimPrefix(line, "worktree ")
			currentBranch = ""
		case strings.HasPrefix(line, "branch refs/heads/"):
			currentBranch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return result
}

// prHandleRe matches names of the form `pr-<N>[-<anything>]`, the
// naming convention used by the pr-review workflow for both worktree
// directories and tmux sessions.
var prHandleRe = regexp.MustCompile(`^pr-[0-9]+(-.*)?$`)

// readReviewWindows parses `tmux list-windows -t pr-reviews
// -F '#{window_name}\t#{@claude-state}'` into a map. `@claude-state`
// is a window option written by the user's Claude Code hooks
// (PermissionRequest → amber, UserPromptSubmit/PostToolUse → working,
// Stop → green, etc.). Absent value → empty string (fresh window).
//
// Only windows of the `pr-reviews` session — that's where the
// consolidated review workflow lives. Other tmux sessions (bf-*, eden)
// aren't PR reviews and don't belong in this overlay.
func readReviewWindows() map[string]string {
	out, err := exec.Command("tmux", "list-windows", "-t", reviewsSessionName, "-F", "#{window_name}\t#{@claude-state}").Output()
	if err != nil {
		return nil // pr-reviews session doesn't exist yet — treat as empty
	}
	result := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		name := parts[0]
		// Skip the scratch keepalive window — no state to report.
		if name == "scratch" {
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
// matching branch names of the form `pr-<N>` or `pr-<N>-<slug>`.
func findLocalForPR(state map[string]LocalState, prNumber int) LocalState {
	exact := fmt.Sprintf("pr-%d", prNumber)
	prefix := exact + "-"
	for name, ls := range state {
		if name == exact || strings.HasPrefix(name, prefix) {
			return ls
		}
	}
	return LocalState{}
}
