// A workspace is three things named identically: a git worktree under
// <repo>/<worktrees_dir>, the branch checked out in it, and a window
// in the multiplexer (mux.go). A review's is `pr-<N>` or
// `pr-<N>-<slug>`; a feature's is the branch Linear names for the
// issue, `<team>-<n>-<slug>`. This file holds what `open`, `close` and
// the TUI overlay share.
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

// issueHandleRe matches a feature's workspace name: the branch Linear
// names for an issue, `<team>-<n>` followed by a slug, in either case.
var issueHandleRe = regexp.MustCompile(`^([A-Za-z]+-[0-9]+)(-.*)?$`)

// issueKeyOf extracts the issue key from a feature's workspace name
// (bar-4159-company-fuzzy-match → BAR-4159), or "" — a review's
// pr-<N> name is never an issue's.
func issueKeyOf(name string) string {
	if prHandleRe.MatchString(name) {
		return ""
	}
	m := issueHandleRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return strings.ToUpper(m[1])
}

// matchesIssue reports whether name is the workspace name for the
// issue with key (BAR-4159).
func matchesIssue(name, key string) bool {
	return issueKeyOf(name) == strings.ToUpper(key)
}

// issueKeyRe is the shape of an issue key: a team's letters, a dash,
// a number.
var issueKeyRe = regexp.MustCompile(`^[A-Za-z]+-[0-9]+$`)

// parseIssueKey validates a positional issue argument and normalises
// its case.
func parseIssueKey(s string) (string, error) {
	if !issueKeyRe.MatchString(s) {
		return "", usageError(fmt.Sprintf("issue key must look like BAR-123, got %q", s))
	}
	return strings.ToUpper(s), nil
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

// lockWorkspace serialises open, start and close on one workspace
// (pr-42, BAR-4159) across processes: a key pressed twice before the
// TUI registered the first, two owl instances, a shell command during
// a TUI open — each would create the worktree; with the lock the
// second waits, then finds it. The lock file lives in the repository's
// git dir, the scope of its worktrees.
func lockWorkspace(repo, label string) (unlock func(), err error) {
	common, err := gitCommonDir(repo)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(common, "owl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, label+".lock"), os.O_CREATE|os.O_RDWR, 0o644)
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
// directory name when that follows a convention, else its branch when
// that does, else "" (not one of owl's workspaces).
func (w worktree) handle() string {
	if base := filepath.Base(w.Path); isWorkspaceName(base) {
		return base
	}
	if isWorkspaceName(w.Branch) {
		return w.Branch
	}
	return ""
}

// isWorkspaceName reports whether a name is a review's or a feature's.
func isWorkspaceName(name string) bool {
	return prHandleRe.MatchString(name) || issueKeyOf(name) != ""
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
