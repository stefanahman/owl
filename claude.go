// Where Claude Code keeps per-project conversation state, and how it
// names the directory for a given working directory. owl reads this
// to decide between starting a fresh review and resuming one.
package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// claudeProjectsDir returns <config dir>/projects, where <config dir>
// is $CLAUDE_CONFIG_DIR or ~/.claude.
func claudeProjectsDir() (string, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".claude")
	}
	return filepath.Join(dir, "projects"), nil
}

// notAlnum is what Claude Code replaces when it names a project's
// state directory after its path.
var notAlnum = regexp.MustCompile(`[^a-zA-Z0-9]`)

// encodeProjectPath mirrors Claude Code's naming of a project's state
// directory: every character of the absolute working directory that is
// not an ASCII letter or digit becomes `-`, so
// /home/me/src/app/.worktrees.local/pr-7 is stored under
// -home-me-src-app--worktrees-local-pr-7.
func encodeProjectPath(cwd string) string {
	return notAlnum.ReplaceAllString(cwd, "-")
}

// hasConversationFor reports whether Claude has a transcript for a
// working directory. The state outlives the directory: `close` removes
// the worktree, `open` recreates it at the same path and resumes.
func hasConversationFor(cwd string) bool {
	projects, err := claudeProjectsDir()
	if err != nil {
		return false
	}
	return hasConversation(filepath.Join(projects, encodeProjectPath(cwd)))
}

// priorWorkspaceName returns the name of a workspace for PR n that
// Claude holds a conversation for — `pr-<n>` or `pr-<n>-<slug>` under
// the repo's worktrees dir — or "" when there is none. `close` removes
// the worktree; `open` recreates it under this name, so the
// conversation resumes even if the PR was retitled in between.
func priorWorkspaceName(repo, worktreesDir string, n int) string {
	if repo == "" {
		return ""
	}
	projects, err := claudeProjectsDir()
	if err != nil {
		return ""
	}
	entries, err := os.ReadDir(projects)
	if err != nil {
		return ""
	}
	base := "pr-" + strconv.Itoa(n)
	prefix := encodeProjectPath(filepath.Join(repo, worktreesDir, base))
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !e.IsDir() || (rest != "" && rest[0] != '-') {
			continue // a file, or another PR (pr-70 when looking for pr-7)
		}
		if hasConversation(filepath.Join(projects, e.Name())) {
			return base + rest // a slug is [a-z0-9-]: the encoding leaves it as is
		}
	}
	return ""
}

// hasPriorConversation reports whether Claude has a conversation for
// PR n's workspace in this repo, whether or not the worktree still
// exists.
func hasPriorConversation(repo, worktreesDir string, n int) bool {
	return priorWorkspaceName(repo, worktreesDir, n) != ""
}

// hasConversation reports whether a project state directory holds at
// least one session transcript (*.jsonl).
func hasConversation(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			return true
		}
	}
	return false
}
