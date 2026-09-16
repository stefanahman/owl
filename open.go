// `owl pr open <N> [--prompt TEXT]`: make sure PR N has a worktree and
// a window in the multiplexer running the agent, select that window,
// and run the after_open hook. Idempotent — re-running selects the
// existing window and, with --prompt, hands the prompt to the running agent.
// `owl pr start <N>` is the same without going there: no window
// selection, no hook — for starting several reviews from the list.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
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
	// Planning reads and creates nothing, so the lock can be taken on
	// the workspace the PR actually resolves to: `owl pr open 4290` and
	// `owl issue open BAR-4157` then take the same lock and cannot both
	// build it. A workspace owl already made answers it locally, and
	// re-opening one asks GitHub nothing, as it never used to.
	plan, found := existingPRWorkspace(cfg, repo, n)
	if !found {
		plan = planPRWorkspace(cfg, repo, currentRepo(cfg.Remote), n)
	}
	unlock, err := lockWorkspace(repo, plan.label(n))
	if err != nil {
		return err
	}
	defer unlock()
	plan, wt, err := ensureWorktree(cfg, repo, n, plan, out)
	if err != nil {
		return err
	}
	env := map[string]string{"OWL_PR": strconv.Itoa(n)}
	if plan.key != "" {
		env["OWL_ISSUE"], env["OWL_BRANCH"] = plan.key, plan.branch
	}
	ws := workspace{
		label: plan.label(n),
		name:  plan.name,
		dir:   wt,
		first: firstPrompt(cfg, n, plan),
		env:   env,
	}
	return ws.open(cfg, newWindows(cfg, scopeForName(plan.name)), repo, prompt, arrive, out)
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
	// args are flags for the agent command, before the prompt. A
	// project passes --session-id or --resume: owl keeps the id of that
	// conversation, so it resumes as itself rather than as whatever
	// `-c` finds most recent in the worktree.
	args []string
}

// open brings the workspace up in the multiplexer: the window with
// the agent started in it, or the prompt handed to the agent already
// there; with arrive, the window selected and the after_open hook run.
func (ws workspace) open(cfg Config, mx windows, repo, prompt string, arrive bool, out io.Writer) error {
	if err := linkLocal(cfg.Agent.LinkLocal, repo, ws.dir); err != nil {
		return err
	}
	if err := mx.Prepare(repo); err != nil {
		return err
	}

	resume := hasConversationFor(ws.dir)
	where := mx.Describe(ws.name)
	// The text the agent starts on: the caller's prompt, or the
	// workspace's own first one when a fresh conversation has none. A
	// resumed conversation with no prompt is sent nothing at all.
	start := prompt
	if !resume && start == "" {
		start = ws.first
	}
	var file promptFile
	if start != "" {
		var err error
		if file, err = writePrompt(ws.name, start); err != nil {
			return fmt.Errorf("%s: writing the prompt: %w", ws.label, err)
		}
	}
	switch {
	case !slices.Contains(mx.Windows(), ws.name):
		if err := mx.Open(ws.name, ws.dir, startLine(cfg.Agent.Cmd, file, resume, ws.args...)); err != nil {
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
		if err := mx.Run(ws.name, startLine(cfg.Agent.Cmd, file, resume, ws.args...)); err != nil {
			return err
		}
		fmt.Fprintf(out, "restarted agent in %s\n", where)
	case prompt != "":
		// Flattened, because this one is typed into a running agent:
		// there is no shell to read a file, a newline is Enter, and the
		// agent would answer a half-written prompt. The start line above
		// needs none of that — the file keeps its newlines.
		if err := mx.Prompt(ws.name, oneLine(prompt)); err != nil {
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

// firstPrompt is what a *fresh* conversation in the workspace opens
// on. A workspace that already holds one resumes it and is sent no
// prompt at all (startLine), so this is the prompt for arriving
// somewhere for the first time and nothing else.
//
// The prompt follows the workspace, like its name and its container:
// `/owl:review` is a procedure for someone else's pull request — it
// ends in a review posted with gh — and pointing it at your own work
// asks the agent to review you. Your own opens on where the work
// stands instead, which is the question the mine pane asks.
func firstPrompt(cfg Config, n int, p prWorkspace) string {
	prompt := cfg.Agent.Prompt
	if p.own {
		prompt = cfg.Mine.Prompt
	}
	return strings.NewReplacer(
		"{pr}", strconv.Itoa(n),
		"{branch}", p.branch,
		"{key}", p.key,
	).Replace(prompt)
}

// prWorkspace is the workspace a PR gets, decided before any of it is
// created.
type prWorkspace struct {
	name   string // the worktree's directory, and the window
	branch string // the branch checked out in it
	key    string // the issue it is filed under, or ""
	// own says the branch is yours and is checked out as itself.
	// Otherwise it is a copy of the PR's head fetched under name, which
	// is what a review of someone else's work wants.
	own bool
}

// label is how messages and the lock name the workspace: the issue
// when there is one, so this and `owl issue open` take the same lock.
func (p prWorkspace) label(n int) string {
	if p.key != "" {
		return p.key
	}
	return "pr-" + strconv.Itoa(n)
}

// existingPRWorkspace is the workspace owl has already made for PR n,
// read off the disk without asking GitHub anything: the directory
// names it, and the branch inside says what it is — only a copy is
// ever checked out under a `pr-<N>` branch, so anything else there is
// your own work.
//
// Re-opening one is the commonest thing the list does and it used to
// cost nothing, so it still does. A PR of yours that resolved to its
// feature is deliberately not found here: that needs the branch, and
// the branch needs GitHub.
func existingPRWorkspace(cfg Config, repo string, n int) (prWorkspace, bool) {
	list, err := listWorktrees(repo)
	if err != nil {
		return prWorkspace{}, false
	}
	for _, wt := range list {
		if wt.Prunable {
			continue
		}
		h := wt.handle()
		if h == "" || !matchesPR(h, n) {
			continue
		}
		// Detached counts as a copy: review worktrees often end up that
		// way, and there is no branch to say otherwise. The handle names
		// it, not the directory: the two differ when the worktree is one
		// somebody else made, and only the handle is a name a scope owns.
		p := prWorkspace{name: h, branch: wt.Branch,
			own: wt.Branch != "" && !matchesPR(wt.Branch, n)}
		if p.own {
			p.key = issueKeyFor(wt.Branch, cfg.Linear.TeamKeys())
		}
		return p, true
	}
	return prWorkspace{}, false
}

// planPRWorkspace decides what PR n's workspace is. slug is the GitHub
// owner/name the PR lives in ("" when unknown).
//
// A PR of yours is your branch, and owl gives you the branch rather
// than a copy of it. The copy was right when every PR in the list was
// someone else's: it reviews the pushed head, and `close` deletes it
// with `branch -D`, which is what a copy is for. On your own work both
// are wrong — the review reads a snapshot while you edit the real tree
// beside it, and the delete takes commits with it.
//
// When the branch carries an issue key, that workspace is the
// feature's, under the name `owl issue open` gives it, so the two
// lists open one window on one worktree with one conversation. When it
// carries none, the workspace keeps the `pr-<N>` name and the reviews
// container — only the branch inside it is real.
//
// Someone else's PR, and your own from a fork, keep the copy: their
// branch is not yours to sit on, and a fork's head is not on your
// remote at all.
func planPRWorkspace(cfg Config, repo, slug string, n int) prWorkspace {
	facts, ok := lookupPR(repo, slug, n)
	named := func() string {
		// The name is decided once, on first open, from the PR title at
		// that time: later opens find the worktree by number and, after
		// `close`, the name Claude's conversation is stored under — so a
		// retitled PR keeps its path, and with it the conversation.
		if prior := priorWorkspaceName(repo, cfg.WorktreesDir, n); prior != "" {
			return prior
		}
		name := "pr-" + strconv.Itoa(n)
		if s := slugify(facts.Title); s != "" {
			name += "-" + s
		}
		return name
	}
	// A copy is what someone else's PR gets, and what owl falls back to
	// when it cannot know better: gh missing, offline, the PR not
	// visible. A fork's head is not on this remote, so there is nothing
	// local to check out even when the PR is yours.
	if !ok || facts.HeadRefName == "" || !facts.Mine || facts.CrossRepo {
		name := named()
		return prWorkspace{name: name, branch: name}
	}
	key := issueKeyFor(facts.HeadRefName, cfg.Linear.TeamKeys())
	if key == "" {
		return prWorkspace{name: named(), branch: facts.HeadRefName, own: true}
	}
	// The branch's own last segment when it already leads with an issue
	// key — everything else finds this workspace by the branch, and a
	// branch leading with BAR-4157 while the PR is filed under the newer
	// BAR-4160 would otherwise be given a name that says one of them
	// twice.
	name := path.Base(facts.HeadRefName)
	if issueKeyOf(name) == "" {
		name = strings.ToLower(key) + "-" + name
	}
	return prWorkspace{name: name, branch: facts.HeadRefName, key: key, own: true}
}

// ensureWorktree makes sure the planned workspace exists and returns
// the plan as it settled, with its path. It runs under the lock and
// looks again before it creates: another owl may have built it between
// the plan and the lock.
//
// The plan comes back corrected rather than as it went in, because
// what is reused may not be what was planned — the branch actually
// checked out is what the prompt and the hook must be told about, not
// the one owl would have used had it built the workspace itself.
func ensureWorktree(cfg Config, repo string, n int, p prWorkspace, out io.Writer) (prWorkspace, string, error) {
	list, err := listWorktrees(repo)
	if err != nil {
		return p, "", err
	}
	// The branch decides, and it is asked first — in a pass of its own,
	// so that a `pr-<N>` copy left over from before does not win by
	// coming earlier in git's list. Your PR's workspace is its branch's,
	// whatever the directory ended up called: the feature the issue list
	// opened, or one you made by hand. Matching on the key instead would
	// land a PR on `bar-4157-part-2` in the worktree holding
	// `bar-4157-part-1`.
	if p.own && p.branch != "" {
		for _, wt := range list {
			if wt.Prunable || wt.Branch != p.branch {
				continue
			}
			if filepath.Dir(wt.Path) != filepath.Join(repo, cfg.WorktreesDir) {
				fmt.Fprintf(out, "%s is checked out outside %s:\n  %s\nopening it there\n", p.branch, cfg.WorktreesDir, wt.Path)
			}
			// The workspace takes the worktree's handle, not its
			// directory name: a worktree somebody else made is called
			// whatever they called it, and a name that is nobody's
			// workspace — neither a PR number nor an issue key — belongs
			// to no scope, so every later call fails to find the window
			// owl is opening right now. Where the directory has no
			// handle the planned name stands; only the path is adopted.
			if h := wt.handle(); h != "" {
				p.name = h
			}
			return p, wt.Path, nil
		}
	}
	for _, wt := range list {
		if wt.Prunable {
			continue
		}
		if h := wt.handle(); h != "" && matchesPR(h, n) {
			// A copy owl made earlier, and the branch in it is not the one
			// the plan named: the prompt and the hook get what is really
			// checked out here. Whose the PR is does not change with it.
			p.name, p.branch = h, wt.Branch
			return p, wt.Path, nil
		}
	}
	path := filepath.Join(repo, cfg.WorktreesDir, p.name)
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return p, "", err
	}
	// A worktree whose directory is gone keeps its registration, and
	// that registration still holds its branch: `worktree add` would
	// refuse the very branch the work is on.
	_, _ = git(repo, "worktree", "prune")
	if !p.own {
		fmt.Fprintf(out, "fetching PR #%d into %s\n", n, path)
		// `+`: the branch is owl's own, and one left behind by a
		// hand-removed worktree may not fast-forward to today's head.
		if _, err := git(repo, "fetch", cfg.Remote, fmt.Sprintf("+pull/%d/head:%s", n, p.branch)); err != nil {
			return p, "", err
		}
		if _, err := git(repo, "worktree", "add", path, p.branch); err != nil {
			return p, "", err
		}
		return p, path, nil
	}
	if _, err := git(repo, "fetch", "--quiet", cfg.Remote); err != nil {
		return p, "", err
	}
	switch {
	case refExists(repo, "refs/heads/"+p.branch):
		fmt.Fprintf(out, "checking out %s into %s\n", p.branch, path)
		_, err = git(repo, "worktree", "add", path, p.branch)
	case refExists(repo, "refs/remotes/"+cfg.Remote+"/"+p.branch):
		fmt.Fprintf(out, "fetching %s/%s into %s\n", cfg.Remote, p.branch, path)
		_, err = git(repo, "worktree", "add", "--track", "-b", p.branch, path, cfg.Remote+"/"+p.branch)
	default:
		// The branch is gone from the remote — deleted after the PR was
		// opened, or never pushed under that name. The PR's head is still
		// a ref, and under the branch's own name it is still the work.
		fmt.Fprintf(out, "fetching PR #%d into %s as %s\n", n, path, p.branch)
		if _, err := git(repo, "fetch", cfg.Remote, fmt.Sprintf("+pull/%d/head:%s", n, p.branch)); err != nil {
			return p, "", err
		}
		_, err = git(repo, "worktree", "add", path, p.branch)
	}
	if err != nil {
		return p, "", err
	}
	return p, path, nil
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

// prFacts is what GitHub says about a PR that decides which workspace
// it gets: whose branch it is, whether that branch is even on this
// remote, and what to call the directory when it is a copy.
type prFacts struct {
	Title       string `json:"title"`
	HeadRefName string `json:"headRefName"`
	CrossRepo   bool   `json:"isCrossRepository"`
	Mine        bool   `json:"viewerDidAuthor"`
}

// prFactsQuery reads the four facts in one round trip. `pr view` can
// answer three of them but not the fourth: it has no viewerDidAuthor,
// so knowing whose the PR is would mean a second call to ask who you
// are, on an action that should feel instant.
const prFactsQuery = `query($owner:String!,$name:String!,$n:Int!){repository(owner:$owner,name:$name){pullRequest(number:$n){title headRefName isCrossRepository viewerDidAuthor}}}`

// lookupPR asks gh about the PR; ok is false when gh is missing or
// fails (offline, unauthenticated), and open then falls back to the
// workspace it can build without knowing anything: a copy at `pr-<N>`.
//
// The repo is passed explicitly when known: gh's own guess fails in a
// clone with several remotes, and with `remote: upstream` it would
// answer for the fork. Without a slug there is no owner and name to
// query with, so that case takes the two-call road.
func lookupPR(repo, slug string, n int) (prFacts, bool) {
	if owner, name, ok := strings.Cut(slug, "/"); ok && owner != "" && name != "" {
		var r struct {
			Data struct {
				Repository struct {
					PullRequest *prFacts `json:"pullRequest"`
				} `json:"repository"`
			} `json:"data"`
		}
		cmd := ghIn(repo, "api", "graphql", "-f", "query="+prFactsQuery,
			"-F", "owner="+owner, "-F", "name="+name, "-F", "n="+strconv.Itoa(n))
		if out, err := cmd.Output(); err == nil {
			if json.Unmarshal(out, &r) == nil && r.Data.Repository.PullRequest != nil {
				return *r.Data.Repository.PullRequest, true
			}
		}
		return prFacts{}, false
	}
	cmd := ghIn(repo, "pr", "view", strconv.Itoa(n), "--json", "title,headRefName,isCrossRepository,author")
	out, err := cmd.Output()
	if err != nil {
		return prFacts{}, false
	}
	var f struct {
		prFacts
		Author struct {
			Login string `json:"login"`
		} `json:"author"`
	}
	if err := json.Unmarshal(out, &f); err != nil {
		return prFacts{}, false
	}
	facts := f.prFacts
	facts.Mine = f.Author.Login != "" && f.Author.Login == currentUser()
	return facts, true
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
// configured command, the workspace's own flags, `-c` to resume a
// prior conversation, then the prompt — the explicit one, or first for
// a fresh conversation. A resumed conversation without an explicit
// prompt gets none: the agent shows the transcript and waits.
//
// `-c` is skipped when the flags already say which conversation to
// resume. A project names its session, and `-c` would reopen whatever
// ran last in that worktree instead.
// startLine is the command line that starts the agent, with the prompt
// read out of promptFile rather than written into the line.
//
// The prompt used to be quoted into the line itself, and the line is
// typed into the workspace's shell one character at a time. Past a few
// thousand characters that loses some of them — not at a threshold, but
// as a race: the same 6000 characters failed and 10000 went through, on
// cmux and on tmux both, each dropping something different. What the
// shell is left holding is a half-typed line whose opening quote never
// closes, so it sits in continuation and the agent never starts.
//
// `"$(cat …)"` types a fixed ~20 characters whatever the prompt holds,
// and the shell reads the file at execution: nothing of the prompt is
// typed, parsed by the line editor, or quoted. It also keeps the
// prompt's own newlines, which the old path had to flatten to survive —
// so an agent now gets the markdown it was written, headings and code
// blocks and all.
// promptFile is where a prompt was written, as a type of its own: the
// parameter it fills used to hold the prompt itself, and text passed
// where a path belongs would otherwise compile and quietly ask the
// shell to `cat` the whole prompt as a filename.
type promptFile string

func startLine(cmd string, prompt promptFile, resume bool, args ...string) string {
	line := cmd
	for _, a := range args {
		line += " " + a
	}
	if resume && !namesAConversation(args) {
		line += " -c"
	}
	if prompt != "" {
		line += ` "$(cat ` + shellQuote(string(prompt)) + `)"`
	}
	return line
}

// writePrompt puts the prompt where the shell can read it back: one
// file per workspace, overwritten on every open, readable only by its
// owner because a prompt carries whatever the issue and the review
// carried.
func writePrompt(workspace, prompt string) (promptFile, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Base, because this is the one place a workspace name becomes a
	// path. The shapes a name may take (`pr-<N>-<anything>`) do not
	// forbid a separator, and nothing else would notice one.
	path := filepath.Join(dir, filepath.Base(workspace)+".md")
	if err := os.WriteFile(path, []byte(prompt), 0o600); err != nil {
		return "", err
	}
	return promptFile(path), nil
}

// namesAConversation reports whether the flags already pick the
// conversation to start or resume.
func namesAConversation(args []string) bool {
	for _, a := range args {
		switch a {
		case "--resume", "-r", "--continue", "-c", "--session-id":
			return true
		}
	}
	return false
}

// removePrompt takes the workspace's prompt file with the workspace.
// It holds whatever the issue and the review held, and a workspace that
// is gone has no use for it.
func removePrompt(workspace string) error {
	dir, err := stateDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, "prompts", filepath.Base(workspace)+".md"))
	if errors.Is(err, os.ErrNotExist) {
		return nil // never opened with a prompt, or already gone
	}
	return err
}

// oneLine folds a prompt onto one line, for the only path that still
// types it: keystrokes to a running agent, where a newline submits.
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
