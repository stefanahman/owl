// A review workspace is three things named identically, `pr-<N>` or
// `pr-<N>-<slug>`: a git worktree under <repo>/<worktrees_dir>, the
// branch checked out in it, and a window in the multiplexer (mux.go).
// This file holds what `open`, `close` and the TUI overlay share.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// prHandleRe matches workspace names: `pr-<N>` optionally followed by
// `-<anything>`.
var prHandleRe = regexp.MustCompile(`^pr-([0-9]+)(-.*)?$`)

// matchesPR reports whether name is the workspace name for PR n.
func matchesPR(name string, n int) bool {
	prefix := "pr-" + strconv.Itoa(n)
	return name == prefix || strings.HasPrefix(name, prefix+"-")
}

// prNumberOf extracts N from a workspace name, or 0.
func prNumberOf(name string) int {
	m := prHandleRe.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// parsePRNumber validates a positional PR argument.
func parsePRNumber(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, usageError(fmt.Sprintf("PR number must be a positive integer, got %q", s))
	}
	return n, nil
}

// git runs a git command in dir and returns trimmed stdout. Errors
// carry git's stderr, which is where the useful message is.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return runOut(cmd)
}

func runOut(cmd *exec.Cmd) (string, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s: %s", strings.Join(cmd.Args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// mainRepo returns the main working tree of the repository containing
// dir — the same answer from the main tree, a subdirectory, or a linked
// worktree, where `--show-toplevel` would return the worktree instead.
// The result has symlinks resolved, like every path git itself prints,
// so it compares equal to `git worktree list` entries.
func mainRepo(dir string) (string, error) {
	common, err := gitCommonDir(dir)
	if err != nil {
		return "", fmt.Errorf("%s is not inside a git repository", dir)
	}
	return filepath.EvalSymlinks(filepath.Dir(common))
}

// gitCommonDir is the repository's shared git directory, absolute —
// the same answer from the main tree and from every linked worktree.
func gitCommonDir(dir string) (string, error) {
	common, err := git(dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common) // git prints it relative to dir
	}
	return filepath.Abs(common)
}

// lockPR serialises open, start and close on one PR across processes:
// a key pressed twice before the TUI registered the first, two pr-owl
// instances, a shell command during a TUI open — each would create the
// worktree; with the lock the second waits, then finds it. The lock
// file lives in the repository's git dir, the scope of its worktrees.
func lockPR(repo string, n int) (unlock func(), err error) {
	common, err := gitCommonDir(repo)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(common, "pr-owl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("pr-%d.lock", n)), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock %s: %w", f.Name(), err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// worktree is one entry of `git worktree list`.
type worktree struct {
	Path   string
	Branch string // short name; "" when detached
}

// handle returns the workspace name a worktree is known by: its
// directory name when that follows the convention, else its branch
// when that does, else "" (not a review workspace).
func (w worktree) handle() string {
	if base := filepath.Base(w.Path); prHandleRe.MatchString(base) {
		return base
	}
	if prHandleRe.MatchString(w.Branch) {
		return w.Branch
	}
	return ""
}

// listWorktrees parses `git worktree list --porcelain` for the repo
// containing dir.
func listWorktrees(dir string) ([]worktree, error) {
	out, err := git(dir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var list []worktree
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			list = append(list, worktree{Path: strings.TrimPrefix(line, "worktree ")})
		case strings.HasPrefix(line, "branch refs/heads/") && len(list) > 0:
			list[len(list)-1].Branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	return list, nil
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
