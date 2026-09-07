// Where Claude Code keeps per-project conversation state, and how it
// names the directory for a given working directory. pr-owl reads this
// to decide between starting a fresh review and resuming one.
package main

import (
	"os"
	"path/filepath"
	"regexp"
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
