// `pr-owl open <N> [--prompt TEXT]`: make sure PR N has a worktree and
// a tmux window running the agent, select that window, and run the
// after_open hook. Idempotent — re-running selects the existing window
// and, with --prompt, hands the prompt to the running agent.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func runOpen(cfg Config, args []string, out io.Writer) error {
	n, prompt, err := parseOpenArgs(args)
	if err != nil {
		return err
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	name, wt, err := ensureWorktree(cfg, repo, n, out)
	if err != nil {
		return err
	}
	if err := linkLocal(cfg.Agent.LinkLocal, repo, wt); err != nil {
		return err
	}
	if err := ensureSession(cfg.Tmux, repo); err != nil {
		return err
	}

	resume := hasConversationFor(wt)
	target := tmuxTarget(cfg.Tmux.Session, name)
	switch {
	case !slices.Contains(reviewWindows(cfg.Tmux.Session), name):
		if _, err := tmux("new-window", "-d", "-t", tmuxTarget(cfg.Tmux.Session, ""), "-c", wt, "-n", name); err != nil {
			return err
		}
		// Freeze the name — otherwise tmux renames the window after the
		// agent process, and the window stops matching the worktree.
		if _, err := tmux("set-option", "-w", "-t", target, "automatic-rename", "off"); err != nil {
			return err
		}
		if err := typeLine(target, agentCommand(cfg.Agent, n, resume, prompt)); err != nil {
			return err
		}
		if resume {
			fmt.Fprintf(out, "started %s, resuming the conversation\n", target)
		} else {
			fmt.Fprintf(out, "started %s\n", target)
		}
	case prompt != "" && paneAtShellPrompt(target):
		// The agent exited; typing the prompt into a shell would run it
		// as a command. Start the agent again with the prompt instead.
		if err := typeLine(target, agentCommand(cfg.Agent, n, resume, prompt)); err != nil {
			return err
		}
		fmt.Fprintf(out, "restarted agent in %s\n", target)
	case prompt != "":
		if err := typeLine(target, prompt); err != nil {
			return err
		}
		fmt.Fprintf(out, "sent prompt to %s\n", target)
	default:
		fmt.Fprintf(out, "selected %s\n", target)
	}
	if _, err := tmux("select-window", "-t", target); err != nil {
		return err
	}
	return runAfterOpen(cfg.Hooks.AfterOpen, out, map[string]string{
		"PR_OWL_PR":       strconv.Itoa(n),
		"PR_OWL_SESSION":  cfg.Tmux.Session,
		"PR_OWL_WINDOW":   name,
		"PR_OWL_WORKTREE": wt,
		"PR_OWL_REPO":     repo,
	})
}

// parseOpenArgs accepts `<N> [--prompt TEXT]` in either order.
func parseOpenArgs(args []string) (n int, prompt string, err error) {
	var num string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--prompt":
			if i+1 == len(args) {
				return 0, "", usageError("open: --prompt needs a value")
			}
			prompt = args[i+1]
			i++
		case strings.HasPrefix(a, "--prompt="):
			prompt = strings.TrimPrefix(a, "--prompt=")
		case strings.HasPrefix(a, "-"):
			return 0, "", usageError("open: unknown flag " + a)
		case num == "":
			num = a
		default:
			return 0, "", usageError("open: unexpected argument " + a)
		}
	}
	if num == "" {
		return 0, "", usageError("open: PR number required")
	}
	n, err = parsePRNumber(num)
	return n, prompt, err
}

// ensureWorktree returns the workspace name and worktree path for PR n,
// creating the branch and worktree when no worktree exists yet.
//
// The name is decided once, on first open, from the PR title at that
// time: later opens find the worktree by number, so a retitled PR keeps
// its path — and with it the agent's per-cwd conversation.
func ensureWorktree(cfg Config, repo string, n int, out io.Writer) (name, path string, err error) {
	list, err := listWorktrees(repo)
	if err != nil {
		return "", "", err
	}
	for _, wt := range list {
		if h := wt.handle(); h != "" && matchesPR(h, n) {
			return h, wt.Path, nil
		}
	}
	name = "pr-" + strconv.Itoa(n)
	if slug := slugify(prTitle(repo, n)); slug != "" {
		name += "-" + slug
	}
	path = filepath.Join(repo, cfg.WorktreesDir, name)
	fmt.Fprintf(out, "fetching PR #%d into %s\n", n, path)
	if _, err := git(repo, "fetch", "origin", fmt.Sprintf("pull/%d/head:%s", n, name)); err != nil {
		return "", "", err
	}
	if _, err := git(repo, "worktree", "add", path, name); err != nil {
		return "", "", err
	}
	return name, path, nil
}

// prTitle asks gh for the PR title; "" when gh is missing or fails
// (offline, unauthenticated) — the workspace is then named `pr-<N>`.
func prTitle(repo string, n int) string {
	cmd := exec.Command("gh", "pr", "view", strconv.Itoa(n), "--json", "title", "--jq", ".title")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// slugify turns a title into a directory-safe suffix: lowercase ASCII
// letters and digits, runs of anything else collapsed to one `-`,
// at most 30 characters.
func slugify(title string) string {
	var b strings.Builder
	dash := true // suppress leading dashes
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	s := b.String() // ASCII only, so byte slicing is safe
	if len(s) > 30 {
		s = s[:30]
	}
	return strings.Trim(s, "-")
}

// linkLocal symlinks the repo's per-user files (the agent.link_local
// globs, relative to the repo root) into the worktree. A worktree
// checks out the branch's tree, so anything gitignored — local
// settings, personal skills — is missing until linked. Entries that
// already exist in the worktree are left alone.
func linkLocal(globs []string, repo, wt string) error {
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(repo, g))
		if err != nil {
			return fmt.Errorf("agent.link_local %q: %w", g, err)
		}
		for _, src := range matches {
			rel, err := filepath.Rel(repo, src)
			if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
				continue
			}
			dst := filepath.Join(wt, rel)
			if _, err := os.Lstat(dst); err == nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureSession creates the review session with its keepalive window
// when it doesn't exist.
func ensureSession(t TmuxConfig, dir string) error {
	if _, err := tmux("has-session", "-t", tmuxTarget(t.Session, "")); err == nil {
		return nil
	}
	_, err := tmux("new-session", "-d", "-s", t.Session, "-n", t.KeepaliveWindow, "-c", dir)
	return err
}

// agentCommand composes the shell line that starts the agent: the
// configured command, `-c` to resume a prior conversation, then the
// prompt — the explicit one, or agent.prompt for a fresh conversation.
// A resumed conversation without an explicit prompt gets none: the
// agent shows the transcript and waits.
func agentCommand(a AgentConfig, n int, resume bool, prompt string) string {
	line := a.Cmd
	if resume {
		line += " -c"
	} else if prompt == "" {
		prompt = strings.ReplaceAll(a.Prompt, "{pr}", strconv.Itoa(n))
	}
	if prompt != "" {
		line += " " + shellQuote(prompt)
	}
	return line
}

// paneAtShellPrompt reports whether the window's foreground process is
// a shell — i.e. the agent isn't running there.
func paneAtShellPrompt(target string) bool {
	out, err := tmux("display-message", "-p", "-t", target, "#{pane_current_command}")
	if err != nil {
		return false
	}
	switch strings.TrimPrefix(filepath.Base(out), "-") {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "nu":
		return true
	}
	return false
}

// typeLine types text into the window as literal keystrokes, then Enter.
func typeLine(target, text string) error {
	if _, err := tmux("send-keys", "-t", target, "-l", text); err != nil {
		return err
	}
	_, err := tmux("send-keys", "-t", target, "Enter")
	return err
}

// runAfterOpen runs the hooks.after_open command through sh with the
// PR_OWL_* variables in its environment, on every open.
func runAfterOpen(hook string, out io.Writer, env map[string]string) error {
	if hook == "" {
		return nil
	}
	cmd := exec.Command("sh", "-c", hook)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("hooks.after_open: %w", err)
	}
	return nil
}
