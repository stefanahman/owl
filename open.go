// `owl pr open <N> [--prompt TEXT]`: make sure PR N has a worktree and
// a window in the multiplexer running the agent, select that window,
// and run the after_open hook. Idempotent — re-running selects the
// existing window and, with --prompt, hands the prompt to the running agent.
// `owl pr start <N>` is the same without going there: no window
// selection, no hook — for starting several reviews from the list.
package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func runOpen(cfg Config, args []string, out io.Writer, arrive bool) error {
	num, prompt, err := parseOpenArgs("pr", "PR number", args)
	if err != nil {
		return err
	}
	n, err := parsePRNumber(num)
	if err != nil {
		return err
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	label := "pr-" + strconv.Itoa(n)
	unlock, err := lockWorkspace(repo, label)
	if err != nil {
		return err
	}
	defer unlock()
	name, wt, err := ensureWorktree(cfg, repo, currentRepo(cfg.Remote), n, out)
	if err != nil {
		return err
	}
	ws := workspace{
		label: label,
		name:  name,
		dir:   wt,
		first: strings.ReplaceAll(cfg.Agent.Prompt, "{pr}", strconv.Itoa(n)),
		env:   map[string]string{"OWL_PR": strconv.Itoa(n)},
	}
	return ws.open(cfg, newWindows(cfg, reviews), repo, prompt, arrive, out)
}

// workspace is what open and start act on once its worktree exists —
// a review's or a feature's: the window named after the worktree, the
// agent's first prompt, and what the hook is told about it.
type workspace struct {
	label string            // how messages name it: pr-42, BAR-4159
	name  string            // the window's and the worktree's name
	dir   string            // the worktree
	first string            // the agent's prompt for a fresh conversation
	env   map[string]string // the scope's variables for after_open
}

// open brings the workspace up in the multiplexer: the window with
// the agent started in it, or the prompt handed to the agent already
// there; with arrive, the window selected and the after_open hook run.
func (ws workspace) open(cfg Config, mx windows, repo, prompt string, arrive bool, out io.Writer) error {
	prompt = oneLine(prompt)
	if err := linkLocal(cfg.Agent.LinkLocal, repo, ws.dir); err != nil {
		return err
	}
	if err := mx.Prepare(repo); err != nil {
		return err
	}

	resume := hasConversationFor(ws.dir)
	where := mx.Describe(ws.name)
	switch {
	case !slices.Contains(mx.Windows(), ws.name):
		if err := mx.Open(ws.name, ws.dir, startLine(cfg.Agent.Cmd, ws.first, resume, prompt)); err != nil {
			return err
		}
		if resume {
			fmt.Fprintf(out, "started %s, resuming the conversation\n", where)
		} else {
			fmt.Fprintf(out, "started %s\n", where)
		}
	case prompt != "" && mx.States()[ws.name] == agentBlocked:
		// Keystrokes would answer the agent's dialog.
		return fmt.Errorf("%s: Claude is waiting for you in %s (a permission or a question) — answer it first", ws.label, where)
	case prompt != "" && mx.AtShell(ws.name):
		// The agent exited; typing the prompt into a shell would run it
		// as a command. Start the agent again with the prompt instead.
		if err := mx.Run(ws.name, startLine(cfg.Agent.Cmd, ws.first, resume, prompt)); err != nil {
			return err
		}
		fmt.Fprintf(out, "restarted agent in %s\n", where)
	case prompt != "":
		if err := mx.Prompt(ws.name, prompt); err != nil {
			return err
		}
		fmt.Fprintf(out, "sent prompt to %s\n", where)
	case arrive:
		fmt.Fprintf(out, "selected %s\n", where)
	default:
		fmt.Fprintf(out, "ready %s\n", where)
	}
	if arrive {
		if err := mx.Select(ws.name); err != nil {
			return err
		}
	}
	hook := cfg.Hooks.AfterOpen.For(mx.Kind())
	if hint := mx.AttachHint(); hint != "" && (hook == "" || !arrive) {
		fmt.Fprintf(out, "attach with: %s\n", hint)
	}
	if !arrive {
		return nil // start: the workspace is up; the caller stays where it is
	}
	env := map[string]string{
		"OWL_WORKTREE": ws.dir,
		"OWL_REPO":     repo,
		"OWL_MUX":      mx.Kind(),
	}
	maps.Copy(env, ws.env)
	maps.Copy(env, mx.Env(ws.name))
	return runAfterOpen(hook, out, env)
}

// parseOpenArgs accepts `<id> [--prompt TEXT]` in either order; noun
// and what name the id in the usage errors (pr, "PR number").
func parseOpenArgs(noun, what string, args []string) (id, prompt string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--prompt":
			if i+1 == len(args) {
				return "", "", usageError(noun + " open: --prompt needs a value")
			}
			prompt = args[i+1]
			i++
		case strings.HasPrefix(a, "--prompt="):
			prompt = strings.TrimPrefix(a, "--prompt=")
		case strings.HasPrefix(a, "-"):
			return "", "", usageError(noun + " open: unknown flag " + a)
		case id == "":
			id = a
		default:
			return "", "", usageError(noun + " open: unexpected argument " + a)
		}
	}
	if id == "" {
		return "", "", usageError(noun + " open: " + what + " required")
	}
	return id, prompt, nil
}

// ensureWorktree returns the workspace name and worktree path for PR n,
// creating the branch and worktree when no worktree exists yet. slug
// is the GitHub owner/name the PR lives in ("" when unknown).
//
// The name is decided once, on first open, from the PR title at that
// time: later opens find the worktree by number and, after `close`, the
// name Claude's conversation is stored under — so a retitled PR keeps
// its path, and with it the agent's per-cwd conversation.
func ensureWorktree(cfg Config, repo, slug string, n int, out io.Writer) (name, path string, err error) {
	list, err := listWorktrees(repo)
	if err != nil {
		return "", "", err
	}
	for _, wt := range list {
		if h := wt.handle(); h != "" && matchesPR(h, n) {
			return h, wt.Path, nil
		}
	}
	if name = priorWorkspaceName(repo, cfg.WorktreesDir, n); name == "" {
		name = "pr-" + strconv.Itoa(n)
		if s := slugify(prTitle(repo, slug, n)); s != "" {
			name += "-" + s
		}
	}
	path = filepath.Join(repo, cfg.WorktreesDir, name)
	fmt.Fprintf(out, "fetching PR #%d into %s\n", n, path)
	// `+`: the branch is owl's own, and one left behind by a
	// hand-removed worktree may not fast-forward to today's head.
	if _, err := git(repo, "fetch", cfg.Remote, fmt.Sprintf("+pull/%d/head:%s", n, name)); err != nil {
		return "", "", err
	}
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return "", "", err
	}
	if _, err := git(repo, "worktree", "add", path, name); err != nil {
		return "", "", err
	}
	return name, path, nil
}

// excludeFromStatus adds the worktrees directory to the repo's
// .git/info/exclude — the per-clone ignore file — so review worktrees
// never show up as untracked in `git status`, without touching the
// project's .gitignore.
func excludeFromStatus(repo, dir string) error {
	common, err := gitCommonDir(repo)
	if err != nil {
		return err
	}
	exclude := filepath.Join(common, "info", "exclude")
	line := "/" + filepath.ToSlash(filepath.Clean(dir)) + "/"
	existing, err := os.ReadFile(exclude)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if slices.Contains(strings.Split(string(existing), "\n"), line) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(exclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	sep := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		sep = "\n"
	}
	_, err = fmt.Fprintf(f, "%s%s\n", sep, line)
	return err
}

// prTitle asks gh for the PR title; "" when gh is missing or fails
// (offline, unauthenticated) — the workspace is then named `pr-<N>`.
// The repo is passed explicitly when known: gh's own guess fails in a
// clone with several remotes, and with `remote: upstream` it would
// answer for the fork.
func prTitle(repo, slug string, n int) string {
	args := []string{"pr", "view", strconv.Itoa(n)}
	if slug != "" {
		args = append(args, "--repo", slug)
	}
	cmd := exec.Command("gh", append(args, "--json", "title", "--jq", ".title")...)
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

// startLine composes the shell line that starts the agent: the
// configured command, `-c` to resume a prior conversation, then the
// prompt — the explicit one, or first for a fresh conversation. A
// resumed conversation without an explicit prompt gets none: the agent
// shows the transcript and waits.
func startLine(cmd, first string, resume bool, prompt string) string {
	line := cmd
	if resume {
		line += " -c"
	} else if prompt == "" {
		prompt = first
	}
	if prompt != "" {
		line += " " + shellQuote(prompt)
	}
	return line
}

// oneLine folds newlines into spaces: the prompt is typed into the
// window as keystrokes, and a newline would submit the first line.
func oneLine(s string) string {
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }), " ")
}

// runAfterOpen runs the hooks.after_open command through sh with the
// OWL_* variables in its environment, on every open.
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
