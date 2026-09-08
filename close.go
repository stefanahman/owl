// `owl pr close [--force] [<N>]`: remove PR N's worktree and branches,
// then close its window in the multiplexer. The container of review
// windows and the agent's conversation on disk both survive — `open`
// resumes it. Uncommitted changes to tracked files stop it unless
// --force. With no number, N is inferred from the current worktree,
// branch, or window, so it can be run from inside the review itself.
// Like open, it acts on the repository of the working directory. The
// removal itself, closeWorkspace, is shared with the features.
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// nothingToCloseError is `close` finding no workspace to remove; main
// maps it to exit status 2 so callers can tell it from a failure.
type nothingToCloseError struct {
	label string // pr-42, BAR-4159
	repo  string
}

func (e nothingToCloseError) Error() string {
	return fmt.Sprintf("no %s workspace in %s (already closed?)", e.label, e.repo)
}

// closeFlags takes --force off the arguments.
func closeFlags(args []string) (force bool, rest []string) {
	for _, a := range args {
		if a == "--force" {
			force = true
		} else {
			rest = append(rest, a)
		}
	}
	return force, rest
}

func runClose(cfg Config, args []string, out io.Writer) error {
	force, rest := closeFlags(args)
	if len(rest) > 1 {
		return usageError("pr close: expected at most one PR number")
	}
	mx := newWindows(cfg, reviews)
	var n int
	var err error
	if len(rest) == 1 {
		n, err = parsePRNumber(rest[0])
	} else {
		n, err = inferPR(mx, cfg.WorktreesDir)
	}
	if err != nil {
		return err
	}
	isPR := func(name string) bool { return matchesPR(name, n) }
	return closeWorkspace(cfg, mx, "pr-"+strconv.Itoa(n), isPR, force, out)
}

// closeWorkspace removes the workspace the match names in the
// repository of the working directory: its worktree under
// worktrees_dir, the local branches named like it (the ones open
// creates; a branch the user switched to meanwhile is theirs), and its
// window. The agent's conversation on disk survives.
func closeWorkspace(cfg Config, mx windows, label string, match func(name string) bool, force bool, out io.Writer) error {
	window := findWindow(mx, match)
	repo, err := mainRepo(".")
	if err != nil {
		return fmt.Errorf("close: not inside a git repository (default_repo makes owl work from anywhere)")
	}
	unlock, err := lockWorkspace(repo, label)
	if err != nil {
		return err
	}
	defer unlock()
	wt := findWorktreeBy(repo, cfg.WorktreesDir, match)
	branches := localBranches(repo, match)
	if wt == "" && len(branches) == 0 && window == "" {
		return nothingToCloseError{label: label, repo: repo}
	}
	// Changed tracked files are work in progress — an experiment, a fix
	// to suggest — and only --force discards them. Untracked files
	// don't count: the links `open` makes are among them.
	if wt != "" && !force {
		if dirty, err := git(wt, "status", "--porcelain", "--untracked-files=no"); err == nil && dirty != "" {
			return fmt.Errorf("%s: uncommitted changes in %s (close --force discards them)", label, wt)
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
		if err := mx.Close(window); err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed = append(failed, "window "+mx.Describe(window))
		} else {
			fmt.Fprintf(out, "closed window %s\n", mx.Describe(window))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%s: could not remove %s", label, strings.Join(failed, ", "))
	}
	fmt.Fprintf(out, "%s closed\n", label)
	return nil
}

// inferPR finds the PR number of the workspace the caller is in: the
// worktree path (survives switching branches inside it), then the
// branch, then the window of the multiplexer.
func inferPR(mx windows, worktreesDir string) (int, error) {
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
	if w, ok := mx.Current(); ok {
		if n := prNumberOf(w); n > 0 {
			return n, nil
		}
	}
	return 0, usageError("pr close: PR number required (or run it from inside a pr-<N> worktree or window)")
}

// findWorktreeBy returns the registered worktree the match names
// under <repo>/<worktreesDir>, by directory name — the branch inside
// may have been switched since open. Worktrees elsewhere are never
// touched, however they are named.
func findWorktreeBy(repo, worktreesDir string, match func(name string) bool) string {
	list, err := listWorktrees(repo)
	if err != nil {
		return ""
	}
	base := filepath.Join(repo, worktreesDir)
	for _, wt := range list {
		if filepath.Dir(wt.Path) == base && match(filepath.Base(wt.Path)) {
			return wt.Path
		}
	}
	return ""
}

// localBranches lists the local branches the match names: the ones
// `open` creates. Any other branch checked out in the worktree
// meanwhile is the user's and is left alone.
func localBranches(repo string, match func(name string) bool) []string {
	out, err := git(repo, "branch", "--list", "--format=%(refname:short)")
	if err != nil {
		return nil
	}
	var list []string
	for _, br := range strings.Fields(out) {
		if match(br) {
			list = append(list, br)
		}
	}
	return list
}
