package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// agentsWorktree is work begun before its issue: a worktree beside the
// repository, as an agent makes one, on a branch of its own with a
// commit on it.
func agentsWorktree(t *testing.T, f *fixture, name, branch string) string {
	t.Helper()
	wt := filepath.Join(f.root, name)
	f.git(f.repo, "worktree", "add", "-q", "-b", branch, wt, "origin/main")
	f.write(filepath.Join(wt, "wip.txt"), "begun before the issue\n")
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", "wip")
	return wt
}

func (f *fixture) adopt(args ...string) (string, error) {
	f.t.Helper()
	var out strings.Builder
	err := runIssue(f.cfg, append([]string{"adopt"}, args...), &out)
	return out.String(), err
}

// claudeIn starts a process named claude working in dir, as Claude
// Code's native build runs, and stops it when the test ends. The
// process is a copy of the test binary running TestHelperClaude: macOS
// kills a copy of one of its own binaries, such as sleep, on start.
func claudeIn(t *testing.T, dir string) int {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, b, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=^TestHelperClaude$")
	cmd.Env = append(os.Environ(), "OWL_HELPER_CLAUDE=1")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd.Process.Pid
}

// TestHelperClaude is not a test of its own: it is what claudeIn's
// process runs, a minute of doing nothing.
func TestHelperClaude(t *testing.T) {
	if os.Getenv("OWL_HELPER_CLAUDE") != "1" {
		t.Skip("run by claudeIn as a process named claude")
	}
	time.Sleep(time.Minute)
}

func TestClaudeRunningIn(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(dir, "wt", "apps")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	pid := claudeIn(t, inside)
	for _, c := range []struct {
		dir  string
		want bool
	}{
		{filepath.Join(dir, "wt", "apps"), true},
		{filepath.Join(dir, "wt"), true},
		{filepath.Join(dir, "w"), false}, // a prefix of the path, not a parent of it
	} {
		got, err := claudeRunningIn(c.dir)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(got, pid) != c.want {
			t.Errorf("claudeRunningIn(%s) = %v, want pid %d listed: %v", c.dir, got, pid, c.want)
		}
	}
}

// TestIssueAdoptMovesTheWorkIn: run from inside the worktree an agent
// made, adopt moves it to the workspace open would have made, renames
// its branch to the one Linear names, takes Claude's conversation along
// and resumes it there.
func TestIssueAdoptMovesTheWorkIn(t *testing.T) {
	f, _ := issueFixture(t)
	from := agentsWorktree(t, f, "repo-cursor", "cursor-flip")
	seedConversation(t, from, "5b1f0c9e-2d4a-4c8b-9e7f-1a3d5c7e9b20")
	args := argsAgent(t, f)
	t.Chdir(from)

	out, err := f.adopt("ENG-4160")
	if err != nil {
		t.Fatal(err)
	}
	name := "eng-4160-per-tenant-override"
	to := filepath.Join(f.repo, ".worktrees.local", name)
	if !f.exists(filepath.Join(to, "wip.txt")) {
		t.Fatalf("the work is not at %s:\n%s", to, out)
	}
	if f.exists(from) {
		t.Error("the worktree was left where the agent made it")
	}
	if got := f.git(to, "branch", "--show-current"); got != name {
		t.Errorf("branch %q, want the one Linear names", got)
	}
	if refExists(f.repo, "refs/heads/cursor-flip") {
		t.Error("the old branch name is still there")
	}
	if up, err := git(to, "rev-parse", "--abbrev-ref", name+"@{upstream}"); err == nil {
		t.Errorf("the renamed branch tracks %s; a feature starts with no upstream", up)
	}
	projects := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects")
	if !f.exists(filepath.Join(projects, encodeProjectPath(to), "5b1f0c9e-2d4a-4c8b-9e7f-1a3d5c7e9b20.jsonl")) {
		t.Errorf("the conversation did not move with the worktree:\n%s", out)
	}
	if f.exists(filepath.Join(projects, encodeProjectPath(from))) {
		t.Error("the old conversation directory is left")
	}
	if got := waitArgs(t, args); !slices.Contains(got, "-c") {
		t.Errorf("agent args %v: the conversation was not resumed", got)
	}
	if !slices.Contains(f.featureWindows(), name) {
		t.Errorf("windows %v: none for the adopted worktree", f.featureWindows())
	}
	if got := readWorktrees(f.cfg)[name].Path; got != to {
		t.Errorf("the list does not see the adopted worktree: %v", readWorktrees(f.cfg))
	}
}

// TestIssueAdoptKeepsAPushedBranch: a branch on the remote keeps its
// name, since a new one would part it from its remote branch and any
// PR on it; the worktree still moves, named with the key in front.
func TestIssueAdoptKeepsAPushedBranch(t *testing.T) {
	f, _ := issueFixture(t)
	from := agentsWorktree(t, f, "repo-cursor", "cursor-flip")
	f.git(from, "push", "-q", "-u", "origin", "cursor-flip")
	t.Chdir(f.repo)

	out, err := f.adopt("ENG-4160", from)
	if err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(f.repo, ".worktrees.local", "eng-4160-cursor-flip")
	if got := f.git(to, "branch", "--show-current"); got != "cursor-flip" {
		t.Errorf("branch %q, want cursor-flip kept", got)
	}
	if !strings.Contains(out, "put ENG-4160 in its PR's title") {
		t.Errorf("output does not say how Linear links a kept branch:\n%s", out)
	}
}

// TestIssueAdoptKeepsABranchThatCarriesTheKey: a branch with the key
// in it, Linear's own with its owner's prefix, is the issue's already;
// only the folder, which names no issue, moves.
func TestIssueAdoptKeepsABranchThatCarriesTheKey(t *testing.T) {
	f, _ := issueFixture(t)
	from := agentsWorktree(t, f, "app-eng-4160", "stefan/eng-4160-credit-flip")
	t.Chdir(from)

	if _, err := f.adopt("eng-4160"); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(f.repo, ".worktrees.local", "eng-4160-credit-flip")
	if got := f.git(to, "branch", "--show-current"); got != "stefan/eng-4160-credit-flip" {
		t.Errorf("branch %q, want it kept", got)
	}
}

// TestIssueAdoptStartsABranchOnADetachedWorktree: work on no branch
// gets the one Linear names, at the commit it had checked out.
func TestIssueAdoptStartsABranchOnADetachedWorktree(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	from := agentsWorktree(t, f, "repo-cursor", "cursor-flip")
	head := f.git(from, "rev-parse", "HEAD")
	f.git(from, "switch", "-q", "--detach")
	f.git(f.repo, "branch", "-D", "cursor-flip")

	if _, err := f.adopt("ENG-4160", from); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(f.repo, ".worktrees.local", "eng-4160-per-tenant-override")
	if got := f.git(to, "branch", "--show-current"); got != "eng-4160-per-tenant-override" {
		t.Errorf("branch %q, want the one Linear names", got)
	}
	if got := f.git(to, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD %s, want the commit the work was on, %s", got, head)
	}
}

// endedPid is the pid of a process that has run and exited.
func endedPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// claudeLock is the lock Claude Code puts on a worktree it creates,
// naming the session's process.
func claudeLock(pid int) string {
	return fmt.Sprintf("claude session probe (pid %d start Tue Oct 20 12:02:31 2026)", pid)
}

// TestIssueAdoptReleasesALockAClaudeSessionLeft: a background or
// subagent session leaves its lock on the worktree when it ends, and
// git refuses to move a locked worktree.
func TestIssueAdoptReleasesALockAClaudeSessionLeft(t *testing.T) {
	f, _ := issueFixture(t)
	t.Chdir(f.repo)
	from := agentsWorktree(t, f, "repo-cursor", "cursor-flip")
	f.git(f.repo, "worktree", "lock", "--reason", claudeLock(endedPid(t)), from)

	out, err := f.adopt("ENG-4160", from)
	if err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(f.repo, ".worktrees.local", "eng-4160-per-tenant-override")
	if !f.exists(filepath.Join(to, "wip.txt")) || !strings.Contains(out, "released the lock") {
		t.Errorf("the stale lock was not released and the work moved:\n%s", out)
	}
	if worktreeAt(f.repo, to).Locked {
		t.Error("the adopted worktree is still locked")
	}
}

// TestIssueAdoptRefuses: each refusal leaves the work where it was, on
// the branch it was on.
func TestIssueAdoptRefuses(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, f *fixture, from string) []string // the adopt arguments
		want  string
	}{
		{"the main checkout", func(t *testing.T, f *fixture, from string) []string {
			return []string{"ENG-4160", f.repo}
		}, "is the main checkout"},
		{"a branch of another issue", func(t *testing.T, f *fixture, from string) []string {
			f.git(from, "branch", "-m", "eng-4170-other")
			return []string{"ENG-4160", from}
		}, "carries ENG-4170, not ENG-4160"},
		{"a workspace the issue has", func(t *testing.T, f *fixture, from string) []string {
			f.openIssue("ENG-4160")
			return []string{"ENG-4160", from}
		}, "ENG-4160 already has a worktree"},
		{"a conversation folder at the other end", func(t *testing.T, f *fixture, from string) []string {
			seedConversation(t, from, "5b1f0c9e-2d4a-4c8b-9e7f-1a3d5c7e9b20")
			// No conversation in it yet, and still a folder a rename
			// cannot land on.
			to := filepath.Join(f.repo, ".worktrees.local", "eng-4160-per-tenant-override")
			if err := os.MkdirAll(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "projects", encodeProjectPath(to), "memory"), 0o755); err != nil {
				t.Fatal(err)
			}
			return []string{"ENG-4160", from}
		}, "move one of"},
		{"Claude running in it", func(t *testing.T, f *fixture, from string) []string {
			claudeIn(t, from)
			return []string{"ENG-4160", from}
		}, "end that session first"},
		{"a lock a running Claude session holds", func(t *testing.T, f *fixture, from string) []string {
			f.git(f.repo, "worktree", "lock", "--reason", claudeLock(os.Getpid()), from)
			return []string{"ENG-4160", from}
		}, fmt.Sprintf("Claude session %d holds", os.Getpid())},
		{"a lock of somebody else's", func(t *testing.T, f *fixture, from string) []string {
			f.git(f.repo, "worktree", "lock", "--reason", "on a removable drive", from)
			return []string{"ENG-4160", from}
		}, `is locked ("on a removable drive")`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, _ := issueFixture(t)
			t.Chdir(f.repo)
			from := agentsWorktree(t, f, "repo-cursor", "cursor-flip")
			args := c.setup(t, f, from)
			branch := f.git(from, "branch", "--show-current")

			_, err := f.adopt(args...)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("adopt: %v, want an error saying %q", err, c.want)
			}
			if !f.exists(filepath.Join(from, "wip.txt")) {
				t.Error("the work moved despite the refusal")
			}
			if got := f.git(from, "branch", "--show-current"); got != branch {
				t.Errorf("branch %q, was %q", got, branch)
			}
		})
	}
}
