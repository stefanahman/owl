// `owl issue adopt <KEY> [<dir>]`: work begun before its issue was
// filed, in a worktree somebody else made — an agent's, beside the
// repository or under .claude/worktrees — moved to the workspace `open`
// would have made for the issue, its conversation with it, then opened
// like any feature.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
)

func runIssueAdopt(cfg Config, tracker Tracker, args []string, out io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return usageError("issue adopt: expected an issue key, and the worktree when it is not the one you are in")
	}
	key, err := parseIssueKey(args[0])
	if err != nil {
		return err
	}
	dir := "."
	if len(args) == 2 {
		dir = args[1]
	}
	if err := adopt(cfg, tracker, key, dir, out); err != nil {
		return fmt.Errorf("issue adopt: %w", err)
	}
	// adopt has released the workspace's lock: open takes it again.
	return runIssueOpen(cfg, tracker, []string{key}, out, true)
}

// adopt moves the worktree holding dir to the issue's place under the
// worktrees dir, on a branch that carries the key, with the
// conversations Claude keeps for it. Everything that can refuse is
// asked before anything moves, so a refusal leaves the work as it was.
func adopt(cfg Config, tracker Tracker, key, dir string, out io.Writer) error {
	repo, err := cfg.repoFor(key)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(repo); err == nil {
		repo = real // git prints resolved paths
	}
	from, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s is not inside a git worktree", dir)
	}
	switch main, err := mainRepo(from); {
	case err != nil:
		return err
	case main != repo:
		return fmt.Errorf("%s is a worktree of %s, and %s's work goes to %s", from, main, key, repo)
	case from == repo:
		return fmt.Errorf("%s is the main checkout: adopt moves a worktree, and the main checkout stays where it is", from)
	}

	unlock, err := lockWorkspace(repo, key)
	if err != nil {
		return err
	}
	defer unlock()
	if w, _, ok := findIssueWorktree(repo, cfg.WorktreesDir, key); ok && w.Path != from {
		return fmt.Errorf("%s already has a worktree, %s: owl issue open %s goes there", key, w.Path, key)
	}
	branch, err := git(from, "branch", "--show-current")
	if err != nil {
		return err
	}
	for _, k := range issueKeysIn(branch) {
		if k != key && cfg.isIssueKey(k) {
			return fmt.Errorf("%s carries %s, not %s: adopt it for that issue", branch, k, key)
		}
	}
	issue, err := tracker.Issue(key)
	if err != nil {
		return err
	}

	// The branch the work ends on. One that carries the key already is
	// the issue's as it is. A pushed one keeps its name: a new one would
	// part it from its remote branch and any PR on it. Any other takes
	// the name Linear gives the issue, which is what links a PR to it.
	target, pushedAs := branch, ""
	switch {
	case branch != "" && branchCarries(branch, key):
	case branch != "" && pushed(repo, cfg.Remote, branch):
		pushedAs = branch
	case issue.Branch == "":
		return fmt.Errorf("%s: Linear names no branch for the issue", key)
	case !matchesIssue(path.Base(issue.Branch), key):
		return fmt.Errorf("%s: Linear's branch %q does not carry the key; owl finds a feature by it", key, issue.Branch)
	default:
		target = issue.Branch
		if refExists(repo, "refs/heads/"+target) {
			return fmt.Errorf("%s exists already, so the work's branch cannot take its name", target)
		}
	}
	to := filepath.Join(repo, cfg.WorktreesDir, worktreeName(target, key))
	if to == from {
		return nil
	}
	if _, err := os.Lstat(to); err == nil {
		return fmt.Errorf("%s exists already", to)
	}
	projects, err := claudeProjectsDir()
	if err != nil {
		return err
	}
	convFrom := filepath.Join(projects, encodeProjectPath(from))
	convTo := filepath.Join(projects, encodeProjectPath(to))
	// A folder at the other end, even one with no conversation in it
	// yet, is one a rename cannot land on.
	if _, err := os.Lstat(convTo); err == nil && hasConversation(convFrom) {
		return fmt.Errorf("both %s and %s have a folder Claude Code keeps conversations in: move one of %s and %s aside first", from, to, convFrom, convTo)
	}
	running, err := claudeRunningIn(from)
	if err != nil {
		return fmt.Errorf("cannot tell whether Claude runs in %s: %w", from, err)
	}
	if len(running) > 0 {
		return fmt.Errorf("a Claude session runs in %s (pid %d): end that session first, so its conversation can move with the worktree", from, running[0])
	}
	// Claude Code locks a worktree it creates, and the lock outlives a
	// background or subagent session that made it: the session's end is
	// what makes it stale. Anyone else's lock is theirs to lift.
	stale := 0
	if wt := worktreeAt(repo, from); wt.Locked {
		pid, ok := claudeLockPid(wt.LockReason)
		switch {
		case !ok:
			return fmt.Errorf("%s is locked (%q): git worktree unlock %s, if nothing needs it held", from, wt.LockReason, from)
		case processAlive(pid):
			return fmt.Errorf("the Claude session %d holds %s (%s): end that session first", pid, from, wt.LockReason)
		}
		stale = pid
	}

	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if stale != 0 {
		if _, err := git(repo, "worktree", "unlock", from); err != nil {
			return err
		}
		fmt.Fprintf(out, "released the lock Claude session %d, which has ended, left on it\n", stale)
	}
	if _, err := git(repo, "worktree", "move", from, to); err != nil {
		return err
	}
	fmt.Fprintf(out, "moved %s\n   to %s\n", from, to)
	switch {
	case branch == "":
		if _, err := git(to, "switch", "-q", "-c", target); err != nil {
			return err
		}
		fmt.Fprintf(out, "started %s at the commit it had checked out\n", target)
	case target != branch:
		if _, err := git(repo, "branch", "-m", branch, target); err != nil {
			return err
		}
		// `worktree add -b x … origin/main` sets main as the upstream,
		// which the rename carries over. open starts a feature with none,
		// so a `git push` cannot land it on main by accident.
		if _, err := git(repo, "rev-parse", "--abbrev-ref", target+"@{upstream}"); err == nil {
			if _, err := git(repo, "branch", "--unset-upstream", target); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, "renamed %s to %s, the branch Linear names for %s\n", branch, target, key)
	case pushedAs != "":
		fmt.Fprintf(out, "kept %s, which is pushed: put %s in its PR's title for Linear to link the two\n", pushedAs, key)
	}
	if hasConversation(convFrom) {
		if err := os.Rename(convFrom, convTo); err != nil {
			return fmt.Errorf("the worktree moved, its conversation did not: %w", err)
		}
		fmt.Fprintf(out, "moved Claude's conversation with it\n")
	}
	return nil
}

// claudeLockRe reads the lock Claude Code puts on a worktree it
// creates: `claude session <name> (pid 7904 start Tue Oct 20 12:02:31 2026)`.
var claudeLockRe = regexp.MustCompile(`^claude session .*\(pid ([0-9]+) start [^)]*\)$`)

// claudeLockPid is the pid of the Claude session a lock names, and
// whether the lock is Claude Code's at all.
func claudeLockPid(reason string) (int, bool) {
	m := claudeLockRe.FindStringSubmatch(reason)
	if m == nil {
		return 0, false
	}
	pid, err := strconv.Atoi(m[1])
	return pid, err == nil
}

// processAlive reports whether a process with the pid exists. Signal 0
// checks without sending anything; EPERM means it exists as another
// user's.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// pushed reports whether the remote has a branch of this one's name.
// An upstream alone says nothing: a branch made from origin/main tracks
// main until it is pushed.
func pushed(repo, remote, branch string) bool {
	return refExists(repo, "refs/remotes/"+remote+"/"+branch)
}
