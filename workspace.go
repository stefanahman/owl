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

// issueKeyRefRe finds an issue key inside a longer name, at a word
// boundary. Unanchored, unlike issueHandleRe: a workspace is named by
// owl and carries the key first, a branch is named by whoever pushed
// it and carries the key wherever they put it — `fix/bar-4157-…` as
// readily as `bar-4157-…`.
var issueKeyRefRe = regexp.MustCompile(`(?i)\b[a-z]+-[0-9]+`)

// issueKeysIn returns every issue key a name mentions, upper-cased and
// deduplicated. A dependency branch's version reads as a key too
// (`sharp-0.35.4` → SHARP-0); nothing ever looks one of those up, so a
// stray costs a map entry and nothing more.
func issueKeysIn(name string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, m := range issueKeyRefRe.FindAllString(name, -1) {
		key := strings.ToUpper(m)
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	return keys
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
	// Prunable is git's own word for a registration whose directory is
	// gone. It stays listed until `git worktree prune` runs, so owl must
	// skip it rather than hand out a path that does not exist.
	Prunable bool
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

// projHandleRe matches a project's workspace name: `proj-` and the
// slug of the project's name.
var projHandleRe = regexp.MustCompile(`^proj-([a-z0-9][a-z0-9-]*)$`)

// projectSlugOf extracts the slug from a project's workspace name
// (proj-sequential-capture-redesign → sequential-capture-redesign),
// or "".
func projectSlugOf(name string) string {
	m := projHandleRe.FindStringSubmatch(name)
	if m == nil {
		return ""
	}
	return m[1]
}

// projectSlug is a project's name as a workspace name: lower case,
// runs of anything else folded to one dash, cut at a dash so a window
// name stays readable. Derived from the name and not from Linear's
// slugId, which is a hex string nobody can read in a window list.
func projectSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
		if i := strings.LastIndexByte(slug, '-'); i > 12 {
			slug = slug[:i]
		}
	}
	return slug
}

// matchesProject reports whether name is the workspace name for the
// project.
func matchesProject(name string, p Project) bool {
	return projectSlugOf(name) == projectSlug(p.Name)
}

// isWorkspaceName reports whether a name is a review's, a feature's or
// a project's.
func isWorkspaceName(name string) bool {
	return prHandleRe.MatchString(name) || issueKeyOf(name) != "" || projectSlugOf(name) != ""
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
		case strings.HasPrefix(line, "prunable") && len(list) > 0:
			list[len(list)-1].Prunable = true
		}
	}
	return list, nil
}

// branchCarries reports whether a branch name mentions the issue key
// anywhere. Not the same question as matchesIssue: a workspace is
// named by owl and leads with the key, a branch is named by whoever
// pushed it — `bar-4098-credit-flip`, `fix/bar-4157-projection`.
func branchCarries(branch, key string) bool {
	key = strings.ToUpper(key)
	for _, k := range issueKeysIn(branch) {
		if k == key {
			return true
		}
	}
	return false
}

// issueBranches lists the branches carrying the key, newest commit
// first, local and the remote's — a remote-only branch by the local
// name it would be checked out under. git keeps the index; this is one
// command, not a walk.
func issueBranches(repo, remote, key string) []string {
	out, err := git(repo, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads", "refs/remotes/"+remote)
	if err != nil {
		return nil
	}
	var names []string
	seen := map[string]bool{}
	for _, ref := range strings.Fields(out) {
		name := strings.TrimPrefix(ref, remote+"/")
		if name == "HEAD" || seen[name] || !branchCarries(name, key) {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// findIssueWorktree returns the worktree already checked out for the
// issue: one of owl's own first, then one someone else made — git
// refuses to check a branch out twice, so a foreign worktree has to be
// used where it stands or not at all. own reports which it found.
func findIssueWorktree(repo, worktreesDir, key string) (w worktree, own, ok bool) {
	list, err := listWorktrees(repo)
	if err != nil {
		return worktree{}, false, false
	}
	base := filepath.Join(repo, worktreesDir)
	var foreign worktree
	var found bool
	for _, w := range list {
		if w.Prunable {
			continue
		}
		if !matchesIssue(filepath.Base(w.Path), key) && !branchCarries(w.Branch, key) {
			continue
		}
		if filepath.Dir(w.Path) == base {
			return w, true, true
		}
		if !found {
			foreign, found = w, true
		}
	}
	return foreign, false, found
}

// heldElsewhere maps each branch checked out in a worktree other than
// the one at except, to that worktree's path. git refuses to delete a
// branch a worktree holds, and a worktree owl is not removing — one
// someone else made — is not owl's to empty.
func heldElsewhere(repo, except string) map[string]string {
	held := map[string]string{}
	list, err := listWorktrees(repo)
	if err != nil {
		return held
	}
	for _, w := range list {
		if w.Prunable || w.Branch == "" || w.Path == except {
			continue
		}
		held[w.Branch] = w.Path
	}
	return held
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
