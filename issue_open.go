// `owl issue open|start <KEY> [--prompt TEXT]` and `owl issue close
// [--force] [<KEY>]`: a feature's workspace — a worktree on the branch
// Linear names for the issue, a window in the features container, the
// agent started on the issue — with the same flow as a review's.
package main

import (
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

func runIssueOpen(cfg Config, tracker Tracker, args []string, out io.Writer, arrive bool) error {
	id, prompt, err := parseOpenArgs("issue", "issue key", args)
	if err != nil {
		return err
	}
	key, err := parseIssueKey(id)
	if err != nil {
		return err
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	unlock, err := lockWorkspace(repo, key)
	if err != nil {
		return err
	}
	defer unlock()
	name, wt, err := ensureIssueWorktree(cfg, repo, key, tracker, out)
	if err != nil {
		return err
	}
	ws := workspace{
		label: key,
		name:  name,
		dir:   wt,
		first: strings.ReplaceAll(cfg.Issue.Prompt, "{key}", key),
		env:   map[string]string{"OWL_ISSUE": key, "OWL_BRANCH": name},
	}
	return ws.open(cfg, newWindows(cfg, features), repo, prompt, arrive, out)
}

// ensureIssueWorktree returns the workspace name and worktree path for
// the issue. It looks for the work that already exists before making
// any of its own, in three steps:
//
//  1. A worktree checked out for the issue — owl's own, else one
//     someone else made, used where it stands.
//  2. A branch carrying the key, local or on the remote. The work on an
//     issue rarely lives on the slug Linear names: it is pushed from
//     `bar-4098-credit-flip-uniform-sets` or `fix/bar-4157-projection`,
//     and creating a fresh branch beside it strands the real one.
//  3. Only when nothing carries the key, the branch Linear names,
//     started from the remote's default branch.
func ensureIssueWorktree(cfg Config, repo, key string, tracker Tracker, out io.Writer) (name, wt string, err error) {
	if w, own, ok := findIssueWorktree(repo, cfg.WorktreesDir, key); ok {
		if !own {
			fmt.Fprintf(out, "%s is checked out outside %s:\n  %s\nopening it there\n", w.Branch, cfg.WorktreesDir, w.Path)
		}
		return filepath.Base(w.Path), w.Path, nil
	}
	if _, err := git(repo, "fetch", "--quiet", cfg.Remote); err != nil {
		return "", "", err
	}
	branch, err := issueBranch(repo, cfg.Remote, key, tracker, out)
	if err != nil {
		return "", "", err
	}
	name = worktreeName(branch, key)
	wt = filepath.Join(repo, cfg.WorktreesDir, name)
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return "", "", err
	}
	// A worktree whose directory is gone — deleted by hand, or a
	// scratchpad the machine cleaned — keeps its registration, and that
	// registration still holds its branch: `worktree add` would refuse
	// the very branch the work is on. Dropping those is what prune is
	// for, and it is a no-op when there are none.
	_, _ = git(repo, "worktree", "prune")
	switch {
	case refExists(repo, "refs/heads/"+branch):
		fmt.Fprintf(out, "checking out %s into %s\n", branch, wt)
		_, err = git(repo, "worktree", "add", wt, branch)
	case refExists(repo, "refs/remotes/"+cfg.Remote+"/"+branch):
		fmt.Fprintf(out, "fetching %s/%s into %s\n", cfg.Remote, branch, wt)
		_, err = git(repo, "worktree", "add", "--track", "-b", branch, wt, cfg.Remote+"/"+branch)
	default:
		base := defaultBranch(repo, cfg.Remote)
		fmt.Fprintf(out, "creating %s from %s/%s into %s\n", branch, cfg.Remote, base, wt)
		_, err = git(repo, "worktree", "add", "--no-track", "-b", branch, wt, cfg.Remote+"/"+base)
	}
	if err != nil {
		return "", "", err
	}
	return name, wt, nil
}

// issueBranch picks the branch the feature works on: the newest of
// those already carrying the key, else the one Linear names. Several
// is the rare case — a stack, a second attempt — and the newest is the
// one being worked on; the others are named so the choice is visible.
func issueBranch(repo, remote, key string, tracker Tracker, out io.Writer) (string, error) {
	if found := issueBranches(repo, remote, key); len(found) > 0 {
		if len(found) > 1 {
			fmt.Fprintf(out, "%s: %d branches carry the key, taking the newest\n", key, len(found))
			for _, b := range found {
				mark := "  "
				if b == found[0] {
					mark = "▸ "
				}
				fmt.Fprintf(out, "%s%s\n", mark, b)
			}
		}
		return found[0], nil
	}
	issue, err := tracker.Issue(key)
	if err != nil {
		return "", err
	}
	if issue.Branch == "" {
		return "", fmt.Errorf("%s: no branch carries the key and Linear names none", key)
	}
	if !matchesIssue(path.Base(issue.Branch), key) {
		return "", fmt.Errorf("%s: Linear's branch %q does not carry the key; owl finds a feature by it", key, issue.Branch)
	}
	return issue.Branch, nil
}

// worktreeName is the directory a branch's workspace gets: the
// branch's last segment when that already leads with the key, else the
// key in front of it — close and the inference find a feature by the
// key in the name.
func worktreeName(branch, key string) string {
	base := path.Base(branch)
	if matchesIssue(base, key) {
		return base
	}
	return strings.ToLower(key) + "-" + base
}

// refExists reports whether the repository has the ref.
func refExists(repo, ref string) bool {
	_, err := git(repo, "rev-parse", "--verify", "--quiet", ref)
	return err == nil
}

// defaultBranch is the remote's default branch: what its HEAD points
// at in the clone, else what gh knows, else main.
func defaultBranch(repo, remote string) string {
	if ref, err := git(repo, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		if br, ok := strings.CutPrefix(ref, remote+"/"); ok && br != "" {
			return br
		}
	}
	if out, err := runOut(ghIn(repo, "repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")); err == nil && out != "" {
		return out
	}
	return "main"
}

func runIssueClose(cfg Config, args []string, out io.Writer) error {
	force, rest := closeFlags(args)
	if len(rest) > 1 {
		return usageError("issue close: expected at most one issue key")
	}
	mx := newWindows(cfg, features)
	var key string
	var err error
	if len(rest) == 1 {
		key, err = parseIssueKey(rest[0])
	} else {
		key, err = inferIssue(mx, cfg.WorktreesDir)
	}
	if err != nil {
		return err
	}
	isIt := func(name string) bool { return matchesIssue(name, key) }
	// A feature's branch may hold commits that exist nowhere else;
	// only --force deletes those. A review's branch is a copy of the
	// PR's, so PR close has no such check.
	if !force {
		if repo, err := mainRepo("."); err == nil {
			for _, br := range localBranches(repo, isIt) {
				if ahead := unpushed(repo, br); ahead != "" {
					return fmt.Errorf("%s: branch %s has %s (close --force deletes them)", key, br, ahead)
				}
			}
		}
	}
	return closeWorkspace(cfg, mx, key, isIt, force, out)
}

// unpushed describes the commits of a branch that its upstream does
// not have — or the branch's whole history when it has no upstream —
// as "3 commits not on origin"; "" when nothing would be lost.
func unpushed(repo, branch string) string {
	upstream, err := git(repo, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		// No upstream: every commit past the remote's default branch is
		// only here.
		if n, err := git(repo, "rev-list", "--count", branch, "--not", "--remotes"); err == nil && n != "0" {
			return n + " commits pushed nowhere"
		}
		return ""
	}
	if n, err := git(repo, "rev-list", "--count", upstream+".."+branch); err == nil && n != "0" {
		return n + " commits not on " + upstream
	}
	return ""
}

// inferIssue finds the key of the feature the caller is in: the
// worktree path, then the branch, then the window of the multiplexer.
func inferIssue(mx windows, worktreesDir string) (string, error) {
	if top, err := git(".", "rev-parse", "--show-toplevel"); err == nil {
		if repo, err := mainRepo("."); err == nil && filepath.Dir(top) == filepath.Join(repo, worktreesDir) {
			if key := issueKeyOf(filepath.Base(top)); key != "" {
				return key, nil
			}
		}
	}
	if br, err := git(".", "branch", "--show-current"); err == nil {
		if key := issueKeyOf(path.Base(br)); key != "" {
			return key, nil
		}
	}
	if w, ok := mx.Current(); ok {
		if key := issueKeyOf(w); key != "" {
			return key, nil
		}
	}
	return "", usageError("issue close: issue key required (or run it from inside a feature's worktree or window)")
}
