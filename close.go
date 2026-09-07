// `pr-owl close [--force] [<N>]`: remove PR N's worktree and branches,
// then kill its tmux window. The review session, its keepalive window
// and the agent's conversation on disk all survive — `open` resumes
// it. Uncommitted changes to tracked files stop it unless --force.
// With no number, N is inferred from the current worktree, branch, or
// tmux window, so it can be run from inside the review itself.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// nothingToCloseError is `close` finding no workspace for the PR; main
// maps it to exit status 2 so callers can tell it from a failure.
type nothingToCloseError struct {
	pr   int
	repo string
}

func (e nothingToCloseError) Error() string {
	return fmt.Sprintf("no pr-%d workspace in %s (already closed?)", e.pr, e.repo)
}

func runClose(cfg Config, args []string, out io.Writer) error {
	var force bool
	var rest []string
	for _, a := range args {
		if a == "--force" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	if len(rest) > 1 {
		return usageError("close: expected at most one PR number")
	}
	var n int
	var err error
	if len(rest) == 1 {
		n, err = parsePRNumber(rest[0])
	} else {
		n, err = inferPR(cfg.WorktreesDir)
	}
	if err != nil {
		return err
	}

	session := cfg.Tmux.Session
	window := findReviewWindow(session, n)
	repo, err := closeRepo(session, window)
	if err != nil {
		return err
	}
	wt := findReviewWorktree(repo, cfg.WorktreesDir, n)
	branches := reviewBranches(repo, n)
	if wt == "" && len(branches) == 0 && window == "" {
		return nothingToCloseError{pr: n, repo: repo}
	}
	// Changed tracked files are the reviewer's work in progress — an
	// experiment, a fix to suggest — and only --force discards them.
	// Untracked files don't count: the links `open` makes are among them.
	if wt != "" && !force {
		if dirty, err := git(wt, "status", "--porcelain", "--untracked-files=no"); err == nil && dirty != "" {
			return fmt.Errorf("pr-%d: uncommitted changes in %s (close --force discards them)", n, wt)
		}
	}

	// We may be running inside the worktree and window being removed:
	// leave the directory first, and outlive the terminal if killing
	// the window takes it away — the git work must complete either way.
	if err := os.Chdir(repo); err != nil {
		return err
	}
	signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)

	// git first, window last: if the window kill ends this process, the
	// important part is already done.
	var failed []string
	if wt != "" {
		if _, err := git(repo, "worktree", "remove", "--force", wt); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = append(failed, "worktree "+wt)
		} else {
			fmt.Fprintf(out, "removed worktree %s\n", wt)
		}
	}
	for _, br := range branches {
		if _, err := git(repo, "branch", "-D", br); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = append(failed, "branch "+br)
		} else {
			fmt.Fprintf(out, "deleted branch %s\n", br)
		}
	}
	if window != "" {
		target := tmuxTarget(session, window)
		if _, err := tmux("kill-window", "-t", target); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = append(failed, "window "+target)
		} else {
			fmt.Fprintf(out, "killed window %s\n", target)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("pr-%d: could not remove %s", n, strings.Join(failed, ", "))
	}
	fmt.Fprintf(out, "pr-%d closed\n", n)
	return nil
}

// inferPR finds the PR number of the workspace the caller is in: the
// worktree path (survives switching branches inside it), then the
// branch, then the tmux window.
func inferPR(worktreesDir string) (int, error) {
	if top, err := git(".", "rev-parse", "--show-toplevel"); err == nil {
		if repo, err := mainRepo("."); err == nil && filepath.Dir(top) == filepath.Join(repo, worktreesDir) {
			if n := prNumberOf(filepath.Base(top)); n > 0 {
				return n, nil
			}
		}
	}
	if br, err := git(".", "branch", "--show-current"); err == nil {
		if n := prNumberOf(br); n > 0 {
			return n, nil
		}
	}
	if os.Getenv("TMUX") != "" {
		if w, err := tmux("display-message", "-p", "#{window_name}"); err == nil {
			if n := prNumberOf(w); n > 0 {
				return n, nil
			}
		}
	}
	return 0, usageError("close: PR number required (or run it from inside a pr-<N> worktree or window)")
}

// closeRepo locates the main repo: from the review window's pane cwd
// when there is one (works from any directory), else from the caller's.
func closeRepo(session, window string) (string, error) {
	if window != "" {
		if dir, err := tmux("display-message", "-p", "-t", tmuxTarget(session, window), "#{pane_current_path}"); err == nil && dir != "" {
			if repo, err := mainRepo(dir); err == nil {
				return repo, nil
			}
		}
	}
	repo, err := mainRepo(".")
	if err != nil {
		return "", fmt.Errorf("not inside a git repository, and no review window to derive it from")
	}
	return repo, nil
}

// findReviewWorktree returns the registered worktree for PR n under
// <repo>/<worktreesDir>, matched by directory name — the branch inside
// may have been switched since open. Worktrees elsewhere are never
// touched, however they are named.
func findReviewWorktree(repo, worktreesDir string, n int) string {
	list, err := listWorktrees(repo)
	if err != nil {
		return ""
	}
	base := filepath.Join(repo, worktreesDir)
	for _, wt := range list {
		if filepath.Dir(wt.Path) == base && matchesPR(filepath.Base(wt.Path), n) {
			return wt.Path
		}
	}
	return ""
}

// reviewBranches lists local branches named pr-<N> or pr-<N>-*: the
// ones `open` creates. Any other branch checked out in the worktree
// meanwhile is the user's and is left alone.
func reviewBranches(repo string, n int) []string {
	out, err := git(repo, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil
	}
	var list []string
	for _, br := range strings.Fields(out) {
		if matchesPR(br, n) {
			list = append(list, br)
		}
	}
	return list
}
