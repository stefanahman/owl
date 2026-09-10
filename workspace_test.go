package main

import (
	"errors"
	"fmt"
	"github.com/stefanahman/mux"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixture is a hermetic environment for open/close: a clone of a local
// origin that has refs/pull/<N>/head for PRs 42 and 7, a fake gh that
// knows PR 42's title, a private tmux server with its own minimal
// config, an empty CLAUDE_CONFIG_DIR, and git isolated from the
// developer's own config (no signing, fixed identity).
type fixture struct {
	t      *testing.T
	root   string // everything lives under here
	origin string
	repo   string // the clone `open` runs in
	cfg    Config
}

// fakeGH writes the fake gh: `api user` answers "you", and `pr view
// 42` answers the facts planPRWorkspace reads. author and head are
// what decide whether PR 42 is one of yours, and so whether open
// fetches a copy or checks the branch out.
func (f *fixture) fakeGH(author, head string) {
	f.t.Helper()
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1 $2" = "api user" ]; then echo you; exit 0; fi
case "$3" in
42) printf '%%s' '{"title":"Fix: Crash on Startup!!","headRefName":"%s","isCrossRepository":false,"author":{"login":"%s"}}' ;;
*) echo 'no such PR' >&2; exit 1;;
esac
`, head, author)
	if err := os.WriteFile(filepath.Join(f.root, "bin", "gh"), []byte(script), 0o755); err != nil {
		f.t.Fatal(err)
	}
}

// prIsYours makes PR 42 one of your own, pushed to head.
func (f *fixture) prIsYours(head string) {
	f.t.Helper()
	f.fakeGH("you", head)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir()) // git prints resolved paths
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, root: root, origin: filepath.Join(root, "origin"), repo: filepath.Join(root, "repo")}

	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "owl")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "owl@example.test")
	}
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))

	// Fake gh: facts for PR 42, failure for anything else. PR 42 is
	// someone else's by default, which is the review the fixture was
	// written for; prIsYours makes it one of your own.
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	f.fakeGH("someone-else", "their-fix")

	// Origin with two "pull requests", then the clone open works in.
	f.git(f.root, "init", "-q", "-b", "main", f.origin)
	f.write(filepath.Join(f.origin, ".gitignore"), ".worktrees.local/\n.claude/\n")
	f.git(f.origin, "add", ".")
	f.git(f.origin, "commit", "-q", "-m", "init")
	for _, pr := range []int{42, 7} {
		f.write(filepath.Join(f.origin, fmt.Sprintf("pr%d.txt", pr)), "change\n")
		f.git(f.origin, "add", ".")
		f.git(f.origin, "commit", "-q", "-m", fmt.Sprintf("PR %d", pr))
		f.git(f.origin, "update-ref", fmt.Sprintf("refs/pull/%d/head", pr), "HEAD")
		f.git(f.origin, "reset", "-q", "--hard", "HEAD~1")
	}
	f.git(f.root, "clone", "-q", f.origin, f.repo)
	f.write(filepath.Join(f.repo, ".claude", "settings.local.json"), "{}\n")
	f.write(filepath.Join(f.repo, ".claude", "skills", "review.local", "SKILL.md"), "# skill\n")

	// Private tmux server with its own minimal config: /bin/sh in every
	// window (the developer's shell would read its rc files and write
	// history into $HOME on exit, racing the temp dir cleanup), and no
	// exit when the last session goes. It owns the default socket name
	// under a private TMUX_TMPDIR, so plain `tmux` reaches it whether or
	// not $TMUX is set. The socket gets a short directory of its own —
	// Unix socket paths are limited to ~100 bytes and t.TempDir includes
	// the test name.
	sockDir, err := os.MkdirTemp("", "owl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	conf := filepath.Join(sockDir, "tmux.conf")
	f.write(conf, "set -g default-shell /bin/sh\nset -s exit-empty off\n")
	t.Setenv("TMUX_TMPDIR", sockDir)
	t.Setenv("TMUX", "") // the test may itself run inside tmux; never touch that server
	t.Setenv("HISTFILE", "")
	// -L creates the socket dir; -S would not. The server starts with a
	// clean environment: the test may run inside a Claude Code session,
	// whose markers the server would pass to every pane.
	start := exec.Command("tmux", "-L", "default", "-f", conf, "start-server")
	start.Env = mux.CleanEnv(os.Environ())
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start-server: %v: %s", err, out)
	}
	socket := filepath.Join(sockDir, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	t.Setenv("TMUX", socket+",0,0") // as inside a pane of the test server
	// The test's multiplexer is that server, whatever the test itself
	// runs in: a herdr pane or a cmux terminal would otherwise be
	// detected, and get the test's windows.
	t.Setenv("HERDR_ENV", "")
	t.Setenv("CMUX_WORKSPACE_ID", "")

	f.cfg = defaultConfig()
	f.cfg.Mux = "tmux"
	f.cfg.Tmux.Session = "reviews"
	f.cfg.Agent.Cmd = "true" // exits at once: the pane is back at a shell prompt
	return f
}

func (f *fixture) git(dir string, args ...string) string {
	f.t.Helper()
	out, err := git(dir, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) tmuxL(args ...string) string {
	f.t.Helper()
	out, err := tmux(args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) open(args ...string) string {
	f.t.Helper()
	var out strings.Builder
	if err := runOpen(f.cfg, args, &out, true); err != nil {
		f.t.Fatalf("open %v: %v", args, err)
	}
	return out.String()
}

// windows lists the review session's tmux windows, the keepalive one
// included: what the session holds, not what owl counts as reviews.
func (f *fixture) windows() []string {
	out := f.tmuxL("list-windows", "-t", mux.TmuxTarget(f.cfg.Tmux.Session, ""), "-F", "#{window_name}")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// tmux runs a tmux command against the test's server.
func tmux(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func (f *fixture) activeWindow() string { return f.activeWindowIn(f.cfg.Tmux.Session) }

// activeWindowIn is the session's current window. (display-message -t
// <session> prints nothing without a client, so ask list-windows.)
func (f *fixture) activeWindowIn(session string) string {
	out, err := tmux("list-windows", "-t", mux.TmuxTarget(session, ""), "-F", "#{window_active} #{window_name}")
	if err != nil {
		f.t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		if name, ok := strings.CutPrefix(line, "1 "); ok {
			return name
		}
	}
	return ""
}

// waitPane polls the window's screen until it contains want. -J joins
// wrapped lines: with a long prompt (macOS's /bin/sh puts the hostname
// in it) the typed command spans two screen lines.
func (f *fixture) waitPane(window, want string) string {
	f.t.Helper()
	return f.waitPaneIn(f.cfg.Tmux.Session, window, want)
}

// waitPaneIn is waitPane for a window of any session.
func (f *fixture) waitPaneIn(session, window, want string) string {
	f.t.Helper()
	target := mux.TmuxTarget(session, window)
	var screen string
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		screen, _ = tmux("capture-pane", "-p", "-J", "-t", target)
		if strings.Contains(screen, want) {
			return screen
		}
	}
	f.t.Fatalf("window %s never showed %q; screen:\n%s", window, want, screen)
	return ""
}

// waitFile polls until the file holds exactly want.
func (f *fixture) waitFile(path, want string) {
	f.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if got, err := os.ReadFile(path); err == nil && string(got) == want {
			return
		}
	}
	got, _ := os.ReadFile(path)
	f.t.Fatalf("%s never held %q; last: %q", path, want, got)
}

func (f *fixture) branches() []string {
	return strings.Fields(f.git(f.repo, "branch", "--list", "--format=%(refname:short)"))
}

func (f *fixture) exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestOpenCreatesWorkspace(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": `echo "$OWL_PR|$OWL_SESSION|$OWL_WINDOW|$OWL_WORKTREE|$OWL_REPO" > ` + hookOut}

	out := f.open("42")

	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	if !strings.Contains(out, "started reviews:"+name) {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "pr42.txt")) {
		t.Fatalf("worktree %s missing the PR's file", wt)
	}
	if head, want := f.git(wt, "rev-parse", "HEAD"), f.git(f.origin, "rev-parse", "refs/pull/42/head"); head != want {
		t.Errorf("worktree HEAD %s, want the PR head %s", head, want)
	}
	if got := f.git(wt, "branch", "--show-current"); got != name {
		t.Errorf("worktree branch %q, want %q", got, name)
	}
	for _, rel := range []string{".claude/settings.local.json", ".claude/skills/review.local"} {
		link, err := os.Readlink(filepath.Join(wt, rel))
		if err != nil || link != filepath.Join(f.repo, rel) {
			t.Errorf("%s: link %q, err %v; want a symlink to the repo's", rel, link, err)
		}
	}
	if !f.exists(filepath.Join(wt, ".claude", "skills", "review.local", "SKILL.md")) {
		t.Error("linked skill dir is not readable through the symlink")
	}
	if got, want := f.windows(), []string{"scratch", name}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows %v, want %v", got, want)
	}
	if got := f.activeWindow(); got != name {
		t.Errorf("active window %q, want %q", got, name)
	}
	if v, _ := tmux("show-options", "-w", "-v", "-t", mux.TmuxTarget("reviews", name), "automatic-rename"); v != "off" {
		t.Errorf("automatic-rename = %q, want off", v)
	}
	if cwd, _ := tmux("display-message", "-p", "-t", mux.TmuxTarget("reviews", name), "#{pane_current_path}"); cwd != wt {
		t.Errorf("window cwd %q, want %q", cwd, wt)
	}
	f.waitPane(name, "true '/owl:review 42'")

	// The TUI overlay sees the window, without the keepalive one, and
	// reads the state option tmux-claude-status writes.
	if got, want := (windows{mux.Tmux{SessionName: f.cfg.Tmux.Session, Keepalive: f.cfg.Tmux.KeepaliveWindow}, reviews, nil}).States(), map[string]string{name: ""}; !reflect.DeepEqual(got, want) {
		t.Errorf("States = %v, want %v", got, want)
	}
	if _, err := tmux("set-option", "-w", "-t", mux.TmuxTarget("reviews", name), "@claude-state", "blocked"); err != nil {
		t.Fatal(err)
	}
	if ls := findLocalForPR(model{cfg: f.cfg}.fetchLocal().(localMsg), 42); ls.Worktree != wt || ls.Window != name || ls.ClaudeState != "blocked" {
		t.Errorf("fetchLocal overlay = %+v", ls)
	}

	hook, err := os.ReadFile(hookOut)
	if err != nil {
		t.Fatalf("after_open hook did not run: %v", err)
	}
	if want := "42|reviews|" + name + "|" + wt + "|" + f.repo + "\n"; string(hook) != want {
		t.Errorf("hook env %q, want %q", hook, want)
	}
	if strings.Contains(out, "attach with") {
		t.Error("no attach hint when after_open is set")
	}

	// The worktrees dir is excluded per clone, so `git status` in the
	// repo stays clean without touching the project's .gitignore.
	if status := f.git(f.repo, "status", "--porcelain"); status != "" {
		t.Errorf("git status not clean after open:\n%s", status)
	}
	exclude, _ := os.ReadFile(filepath.Join(f.repo, ".git", "info", "exclude"))
	if !strings.Contains(string(exclude), "\n/.worktrees.local/\n") && !strings.HasPrefix(string(exclude), "/.worktrees.local/\n") {
		t.Errorf(".git/info/exclude lacks the worktrees dir:\n%s", exclude)
	}
	f.open("7")
	if n := strings.Count(string(mustRead(t, filepath.Join(f.repo, ".git", "info", "exclude"))), "/.worktrees.local/"); n != 1 {
		t.Errorf("exclude line added %d times, want once", n)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestStartStaysPut(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": "touch " + hookOut}

	var out strings.Builder
	if err := runOpen(f.cfg, []string{"42"}, &out, false); err != nil {
		t.Fatal(err)
	}

	name := "pr-42-fix-crash-on-startup"
	if !strings.Contains(out.String(), "started reviews:"+name) {
		t.Errorf("output: %q", out.String())
	}
	if !f.exists(filepath.Join(f.repo, ".worktrees.local", name, "pr42.txt")) {
		t.Error("start did not create the worktree")
	}
	f.waitPane(name, "true '/owl:review 42'")
	if got := f.activeWindow(); got != "scratch" {
		t.Errorf("start selected the window (%q); it must stay where it was", got)
	}
	if f.exists(hookOut) {
		t.Error("start ran the after_open hook")
	}

	out.Reset()
	if err := runOpen(f.cfg, []string{"42"}, &out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ready reviews:"+name) {
		t.Errorf("second start: %q", out.String())
	}
	// f goes through start too: the prompt reaches the window, nothing
	// is selected, no hook.
	out.Reset()
	if err := runOpen(f.cfg, []string{"42", "--prompt", "look again"}, &out, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "restarted agent") || f.activeWindow() != "scratch" || f.exists(hookOut) {
		t.Errorf("start --prompt: %q, active %q, hook ran %v", out.String(), f.activeWindow(), f.exists(hookOut))
	}

	// open afterwards goes there: selects the window, runs the hook.
	f.open("42")
	if got := f.activeWindow(); got != name || !f.exists(hookOut) {
		t.Errorf("open after start: active %q, hook ran %v", got, f.exists(hookOut))
	}
}

func TestLockPR(t *testing.T) {
	f := newFixture(t)
	unlock, err := lockWorkspace(f.repo, "pr-42")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.Open(filepath.Join(f.repo, ".git", "owl", "pr-42.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("a second owl on the same PR while the first works: %v, want EWOULDBLOCK", err)
	}
	unlock()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	// Another PR is not held up.
	other, err := lockWorkspace(f.repo, "pr-7")
	if err != nil {
		t.Fatal(err)
	}
	other()
}

func TestOpenOutsideTmuxHintsAttach(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	t.Setenv("TMUX", "") // a plain terminal; the server is still the fixture's

	out := f.open("42")
	if !strings.Contains(out, "attach with: tmux attach -t reviews") {
		t.Errorf("output lacks the attach hint: %q", out)
	}
}

func TestOpenFetchesFromTheConfiguredRemote(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.git(f.repo, "remote", "rename", "origin", "upstream")
	f.git(f.repo, "remote", "add", "origin", filepath.Join(f.root, "nowhere"))

	var out strings.Builder
	if err := runOpen(f.cfg, []string{"42"}, &out, true); err == nil {
		t.Fatal("open should fail when the default remote has no such PR")
	}
	f.cfg.Remote = "upstream"
	f.open("42")
	if !f.exists(filepath.Join(f.repo, ".worktrees.local", "pr-42-fix-crash-on-startup", "pr42.txt")) {
		t.Error("worktree not created from the upstream remote")
	}
	if got := currentRepo("upstream"); got != "" { // a local path is not a GitHub URL
		t.Errorf("currentRepo on a local-path remote = %q, want empty", got)
	}
}

func TestParseRepoURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:acme/app.git":          "acme/app",
		"git@github.com:acme/app":              "acme/app",
		"https://github.com/acme/app.git":      "acme/app",
		"https://github.com/acme/app":          "acme/app",
		"https://github.com/acme/app/":         "acme/app",
		"ssh://git@github.com/acme/app.git":    "acme/app",
		"ssh://git@ghe.example.com:22/o/r.git": "o/r",
		"https://ghe.example.com/o/r\n":        "o/r",
		"/Users/me/src/app":                    "",
		"../origin":                            "",
		"file:///tmp/a/b":                      "",
		"app":                                  "",
	}
	for url, want := range cases {
		if got := parseRepoURL(url); got != want {
			t.Errorf("parseRepoURL(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	// The agent leaves a line per start; the screen can't be counted —
	// a readline shell echoes a typed-ahead command twice (macOS's sh).
	runs := filepath.Join(f.root, "agent.runs")
	f.cfg.Agent.Cmd = "echo run >> " + runs + " #" // the prompt lands after # and is ignored
	f.open("42")
	name := "pr-42-fix-crash-on-startup"
	f.waitFile(runs, "run\n")
	_, _ = tmux("select-window", "-t", mux.TmuxTarget("reviews", "scratch"))

	out := f.open("42")

	if !strings.Contains(out, "selected reviews:"+name) {
		t.Errorf("output: %q", out)
	}
	if got := f.windows(); len(got) != 2 {
		t.Errorf("second open added a window: %v", got)
	}
	if got := f.activeWindow(); got != name {
		t.Errorf("active window %q, want %q", got, name)
	}
	time.Sleep(300 * time.Millisecond) // long enough for a second start to have left its line
	if got := string(mustRead(t, runs)); got != "run\n" {
		t.Errorf("agent starts after two opens: %q, want one", got)
	}
}

func TestOpenFromInsideAWorktreeUsesTheMainRepo(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("42")
	t.Chdir(filepath.Join(f.repo, ".worktrees.local", "pr-42-fix-crash-on-startup"))

	out := f.open("7") // gh knows nothing about 7 → bare name

	if !strings.Contains(out, "started reviews:pr-7\n") {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(f.repo, ".worktrees.local", "pr-7", "pr7.txt")) {
		t.Error("worktree for PR 7 not under the main repo's worktrees dir")
	}
}

func TestOpenPromptHandling(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)

	// Fresh window with an explicit prompt: it replaces agent.prompt.
	f.open("42", "--prompt", "look again")
	name := "pr-42-fix-crash-on-startup"
	f.waitPane(name, "true 'look again'")

	// The agent exited (true returns at once) and a conversation exists
	// on disk: a later prompt must restart the agent with -c, not be
	// typed into the shell.
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	f.write(filepath.Join(f.root, "claude", "projects", encodeProjectPath(wt), "s.jsonl"), "{}\n")
	out := f.open("42", "--prompt=it's back")
	if !strings.Contains(out, "restarted agent") {
		t.Errorf("output: %q", out)
	}
	f.waitPane(name, `true -c 'it'\''s back'`)

	// A running agent gets the prompt as keystrokes.
	f.cfg.Agent.Cmd = "cat >/dev/null #" // stays in the foreground; everything after # is ignored
	f.open("7")
	f.waitPane("pr-7", "cat >/dev/null # '/owl:review 7'")
	out = f.open("7", "--prompt", "ping")
	if !strings.Contains(out, "sent prompt") {
		t.Errorf("output: %q", out)
	}
	if screen := f.waitPane("pr-7", "ping"); strings.Contains(screen, "'ping'") {
		t.Errorf("prompt was wrapped in an agent command:\n%s", screen)
	}

	// A prompt is one line of keystrokes: a newline would submit early.
	f.open("7", "--prompt", "first\nsecond")
	f.waitPane("pr-7", "first second")

	// Claude waiting on the user: the keystrokes would answer its dialog.
	if _, err := tmux("set-option", "-w", "-t", mux.TmuxTarget("reviews", "pr-7"), mux.ClaudeStateOption, "blocked"); err != nil {
		t.Fatal(err)
	}
	if err := runOpen(f.cfg, []string{"7", "--prompt", "again"}, io.Discard, true); err == nil || !strings.Contains(err.Error(), "waiting for you") {
		t.Errorf("prompt into a blocked window: %v, want a refusal", err)
	}
	if _, err := tmux("set-option", "-wu", "-t", mux.TmuxTarget("reviews", "pr-7"), mux.ClaudeStateOption); err != nil {
		t.Fatal(err)
	}

	// Without a prompt, an existing window is only selected.
	_, _ = tmux("select-window", "-t", mux.TmuxTarget("reviews", "scratch"))
	if out := f.open("7"); !strings.Contains(out, "selected") || f.activeWindow() != "pr-7" {
		t.Errorf("output %q, active %q", out, f.activeWindow())
	}
}

func TestOpenResumesAfterClose(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("42")
	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	f.write(filepath.Join(f.root, "claude", "projects", encodeProjectPath(wt), "s.jsonl"), "{}\n")
	if err := runClose(f.cfg, []string{"42"}, io.Discard); err != nil {
		t.Fatal(err)
	}

	// Retitled since: the workspace keeps the name the conversation is
	// stored under, whatever gh says the title is now.
	f.write(filepath.Join(f.root, "bin", "gh"), "#!/bin/sh\necho 'Renamed since'\n")

	out := f.open("42")

	if !strings.Contains(out, "started reviews:"+name) || !strings.Contains(out, "resuming the conversation") {
		t.Errorf("output: %q", out)
	}
	f.waitPane(name, "true -c\n") // no prompt: the agent shows the transcript and waits
}

func TestClose(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("42")
	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	f.waitPane(name, "true")

	// Work in progress in the worktree stops close; --force discards it.
	f.write(filepath.Join(wt, "pr42.txt"), "edited during the review\n")
	if err := runClose(f.cfg, []string{"42"}, io.Discard); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Fatalf("close on a dirty worktree: %v, want a refusal", err)
	}
	if !f.exists(wt) {
		t.Fatal("the refusal removed the worktree")
	}
	var out strings.Builder
	if err := runClose(f.cfg, []string{"--force", "42"}, &out); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"removed worktree " + wt, "deleted branch " + name, "closed window reviews:" + name, "pr-42 closed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if f.exists(wt) {
		t.Error("worktree still on disk")
	}
	if slices.Contains(f.branches(), name) {
		t.Error("branch still exists")
	}
	if got, want := f.windows(), []string{"scratch"}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows %v, want %v (session and keepalive survive)", got, want)
	}

	err := runClose(f.cfg, []string{"42"}, &out)
	var nothing nothingToCloseError
	if !errors.As(err, &nothing) {
		t.Errorf("second close: got %v, want nothingToCloseError", err)
	}
}

func TestClosePartialWorkspaces(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)

	// Only the window is left (worktree and branch removed by hand).
	f.open("42")
	name := "pr-42-fix-crash-on-startup"
	f.waitPane(name, "true")
	f.git(f.repo, "worktree", "remove", "--force", filepath.Join(f.repo, ".worktrees.local", name))
	f.git(f.repo, "branch", "-D", name)
	if err := runClose(f.cfg, []string{"42"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.windows(), name) {
		t.Error("window survived close")
	}

	// Only the worktree is left (window killed by hand); the repo is
	// found from the caller's cwd.
	f.open("7")
	f.waitPane("pr-7", "true")
	if _, err := tmux("kill-window", "-t", mux.TmuxTarget("reviews", "pr-7")); err != nil {
		t.Fatal(err)
	}
	if err := runClose(f.cfg, []string{"7"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.exists(filepath.Join(f.repo, ".worktrees.local", "pr-7")) || slices.Contains(f.branches(), "pr-7") {
		t.Error("worktree or branch survived close")
	}
}

func TestCloseInfersThePRFromTheCwd(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("7")
	f.waitPane("pr-7", "true")
	wt := filepath.Join(f.repo, ".worktrees.local", "pr-7")

	// From inside the worktree, on a different branch.
	f.git(wt, "checkout", "-q", "-b", "my-experiment")
	t.Chdir(wt)
	if err := runClose(f.cfg, nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if f.exists(wt) || slices.Contains(f.branches(), "pr-7") || slices.Contains(f.windows(), "pr-7") {
		t.Error("workspace survived close")
	}
	if !slices.Contains(f.branches(), "my-experiment") {
		t.Error("close deleted a branch it did not create")
	}

	// Outside any repo, with nothing to go on.
	t.Chdir(f.root)
	err := runClose(f.cfg, nil, io.Discard)
	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("got %v, want a usageError", err)
	}
}

func TestCloseActsOnTheCurrentRepo(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	f.open("42")
	name := "pr-42-fix-crash-on-startup"
	f.waitPane(name, "true")

	// Outside any repo there is nothing to act on — the review window's
	// own directory is not consulted (a shell that cd'd elsewhere would
	// point close at another repo's pr-42 branches).
	t.Chdir(f.root)
	err := runClose(f.cfg, []string{"42"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not inside a git repository") {
		t.Fatalf("close outside a repo: %v, want a refusal", err)
	}
	if !f.exists(filepath.Join(f.repo, ".worktrees.local", name)) {
		t.Error("the refusal removed the worktree")
	}
	t.Chdir(f.repo)
	if err := runClose(f.cfg, []string{"42"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Fix: Crash on Startup!!":                   "fix-crash-on-startup",
		"  --leading and trailing--  ":              "leading-and-trailing",
		"Ünïcödé — títle":                           "n-c-d-t-tle",
		"a very long title that keeps going and on": "a-very-long-title-that-keeps-g",
		"exactly-thirty-characters-here":            "exactly-thirty-characters-here",
		"cut-right-before-a-dash-boundary-x":        "cut-right-before-a-dash-bounda",
		"":                                          "",
		"!!!":                                       "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStartLine(t *testing.T) {
	cmd, first := "claude --permission-mode auto", "/review 42"
	cases := []struct {
		resume bool
		prompt string
		want   string
	}{
		{false, "", "claude --permission-mode auto '/review 42'"},
		{false, "hi", "claude --permission-mode auto 'hi'"},
		{true, "", "claude --permission-mode auto -c"},
		{true, "it's", `claude --permission-mode auto -c 'it'\''s'`},
	}
	for _, c := range cases {
		if got := startLine(cmd, first, c.resume, c.prompt); got != c.want {
			t.Errorf("startLine(resume=%v, %q) = %q, want %q", c.resume, c.prompt, got, c.want)
		}
	}

	// A project names its own conversation. The flags come before the
	// prompt, and `-c` is not added on top: it would resume whatever ran
	// last in the worktree instead of the session owl is holding.
	fresh := startLine(cmd, first, false, "", "--session-id", "u-u-i-d")
	if want := "claude --permission-mode auto --session-id u-u-i-d '/review 42'"; fresh != want {
		t.Errorf("fresh project start = %q, want %q", fresh, want)
	}
	again := startLine(cmd, first, true, "", "--resume", "u-u-i-d")
	if want := "claude --permission-mode auto --resume u-u-i-d"; again != want {
		t.Errorf("resumed project = %q, want %q", again, want)
	}
	// Without such a flag the old behaviour stands.
	if got := startLine(cmd, first, true, "", "--add-dir", "/wt"); got != "claude --permission-mode auto --add-dir /wt -c" {
		t.Errorf("flags that name no conversation = %q", got)
	}
}

func TestProjectSlug(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Sequential Capture redesign", "sequential-capture-redesign"},
		// Over 32 characters it cuts at the last dash, so a window name
		// ends on a whole word.
		{"Emission Categories — Plumbing / data modelling", "emission-categories-plumbing"},
		{"Sven v2 — Deterministic harness + autofix", "sven-v2-deterministic-harness"},
		{"Bardo Backstage (BACKEND / bardo-system)", "bardo-backstage-backend-bardo"},
		{"  ", ""},
	}
	for _, c := range cases {
		if got := projectSlug(c.name); got != c.want {
			t.Errorf("projectSlug(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	// The workspace name round-trips, and does not collide with the
	// other two kinds.
	name := "proj-" + projectSlug("Sequential Capture redesign")
	if projectSlugOf(name) != "sequential-capture-redesign" {
		t.Errorf("projectSlugOf(%q) = %q", name, projectSlugOf(name))
	}
	if !isWorkspaceName(name) || issueKeyOf(name) != "" || prNumberOf(name) != 0 {
		t.Errorf("%q is not cleanly a project's workspace name", name)
	}
	if projectSlugOf("pr-42") != "" || projectSlugOf("bar-4159-thing") != "" {
		t.Error("a review's or a feature's name reads as a project's")
	}
}

func TestParseOpenArgs(t *testing.T) {
	ok := map[string][]string{
		"42":           {"42"},
		"42 --prompt":  {"42", "--prompt", "hi"},
		"prompt first": {"--prompt", "hi", "42"},
		"equals":       {"42", "--prompt=hi"},
	}
	for name, args := range ok {
		id, prompt, err := parseOpenArgs("pr", "PR number", args)
		if err != nil || id != "42" || (len(args) > 1 && prompt != "hi") {
			t.Errorf("%s: got %q %q %v", name, id, prompt, err)
		}
	}
	for _, args := range [][]string{nil, {"42", "7"}, {"42", "--bogus"}, {"42", "--prompt"}} {
		if _, _, err := parseOpenArgs("pr", "PR number", args); err == nil {
			t.Errorf("%v: expected an error", args)
		}
	}
	// The id's shape is the scope's business: a PR number must be one.
	for _, bad := range []string{"x", "0", "-1"} {
		if _, err := parsePRNumber(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

func TestMatchesPR(t *testing.T) {
	if !matchesPR("pr-4", 4) || !matchesPR("pr-4-slug", 4) || matchesPR("pr-42", 4) || matchesPR("pr-4x", 4) || matchesPR("xpr-4", 4) {
		t.Error("matchesPR boundaries are wrong")
	}
	if prNumberOf("pr-12-fix") != 12 || prNumberOf("pr-12") != 12 || prNumberOf("pr-x") != 0 || prNumberOf("main") != 0 {
		t.Error("prNumberOf is wrong")
	}
}

func TestMainRepo(t *testing.T) {
	f := newFixture(t)
	f.git(f.repo, "worktree", "add", "-q", filepath.Join(f.repo, ".worktrees.local", "pr-1"), "-b", "pr-1")
	sub := filepath.Join(f.repo, ".worktrees.local", "pr-1", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{f.repo, filepath.Join(f.repo, ".claude"), filepath.Join(f.repo, ".worktrees.local", "pr-1"), sub} {
		got, err := mainRepo(dir)
		if err != nil || got != f.repo {
			t.Errorf("mainRepo(%s) = %q, %v; want %q", dir, got, err, f.repo)
		}
	}
	if _, err := mainRepo(f.root); err == nil {
		t.Error("mainRepo outside a repo should fail")
	}
}
