// Where Claude Code keeps per-project conversation state, and how it
// names the directory for a given working directory. owl reads this
// to decide between starting a fresh review and resuming one.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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

// hasSession reports whether Claude holds the conversation with this
// id for a working directory: its transcript is named for the session.
// A project's conversation is known by its id, so another transcript
// in the same directory does not make it resumable.
func hasSession(cwd, session string) bool {
	if session == "" {
		return false
	}
	projects, err := claudeProjectsDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(projects, encodeProjectPath(cwd), session+".jsonl"))
	return err == nil
}

// sessionWorkspace is the project workspace under the repo's worktrees
// dir whose conversation directory holds the session, under whatever
// proj- name it has, or "". It is how a project renamed more than once
// finds its conversation: neither the name of its first open nor its
// current one may be the name it was last opened under.
func sessionWorkspace(repo, worktreesDir, session string) string {
	if repo == "" || session == "" {
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
	prefix := encodeProjectPath(filepath.Join(repo, worktreesDir, "proj-"))
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok || !e.IsDir() || rest == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(projects, e.Name(), session+".jsonl")); err == nil {
			return "proj-" + rest // a slug is [a-z0-9-]: the encoding leaves it as is
		}
	}
	return ""
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

// claudeRunningIn lists the Claude Code processes working in dir or
// below it. A session whose directory moves out from under it breaks,
// and it writes its transcript while it runs, so neither may move. It
// finds Claude by its process name, claude, as the native build runs.
func claudeRunningIn(dir string) ([]int, error) {
	out, err := exec.Command("pgrep", "-x", "claude").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil // none running
		}
		return nil, fmt.Errorf("pgrep: %w", err)
	}
	var pids []int
	for _, f := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			continue
		}
		if cwd := processCwd(pid); cwd == dir || strings.HasPrefix(cwd, dir+string(filepath.Separator)) {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// processCwd is a process's working directory, from /proc where there
// is one and lsof where there is not (macOS); "" when neither can say,
// as for another user's process.
func processCwd(pid int) string {
	if cwd, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/cwd"); err == nil {
		return cwd
	}
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if cwd, ok := strings.CutPrefix(line, "n"); ok {
			return cwd
		}
	}
	return ""
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
