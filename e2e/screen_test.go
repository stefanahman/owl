// Package e2e runs the built pr-owl binary in a virtual terminal and
// snapshots what it draws. It is a separate package because vttest's
// snapshot helper and teatest's golden helper both register a global
// -update flag and cannot share a test binary.
package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vttest"
	"github.com/charmbracelet/x/vttest/snapshot"
)

// TestScreen runs the real binary in a virtual terminal and snapshots
// what it draws: the PR list and the help modal. The snapshots live in
// testdata/ as JSON (compared) and PNG (for humans); refresh them with
// `go test ./e2e -update`.
//
// Everything the TUI talks to is faked: a git repo whose origin points
// at github.com, a gh on PATH that serves fixture JSON, a private tmux
// server, an empty Claude config dir, and a config with one link.
func TestScreen(t *testing.T) {
	root := t.TempDir()
	repo := fixtureRepo(t, root)
	fakeGH(t, root)
	env := hermeticEnv(t, root)

	term, err := vttest.NewTerminal(t, 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	cmd := start(t, term, repo, env)

	waitFor(t, term, "Waiting for author")
	snapshot.TestdataEqual(t, "list", term)

	term.SendKey(uv.KeyPressEvent{Code: '?', Text: "?"})
	waitFor(t, term, "pr-owl · help")
	snapshot.TestdataEqual(t, "help", term)

	term.SendKey(uv.KeyPressEvent{Code: 'q', Text: "q"})
	waitExit(t, term, cmd)
}

// TestOpenFromTheTUI presses Enter on the first PR: the binary runs
// its own `open`, which fetches the PR into a worktree, creates the
// review window on the private tmux server and — on_open: quit — ends
// the TUI with the child's summary printed.
func TestOpenFromTheTUI(t *testing.T) {
	root := t.TempDir()
	repo := fixtureRepo(t, root)
	fakeGH(t, root)
	env := hermeticEnv(t, root)

	term, err := vttest.NewTerminal(t, 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()
	cmd := start(t, term, repo, env)
	waitFor(t, term, "Waiting for author")
	term.SendKey(uv.KeyPressEvent{Code: uv.KeyEnter})
	waitExit(t, term, cmd)
	if !strings.Contains(screenText(term), "started =pr-reviews:=pr-3543-add-billing-migration") {
		t.Errorf("no farewell on screen:\n%s", screenText(term))
	}
	wt := filepath.Join(repo, ".worktrees.local", "pr-3543-add-billing-migration")
	if _, err := os.Stat(filepath.Join(wt, ".git")); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
	tmux := exec.Command("tmux", "list-windows", "-t", "=pr-reviews", "-F", "#{window_name}")
	tmux.Env = env
	out, err := tmux.Output()
	if err != nil || !strings.Contains(string(out), "pr-3543-add-billing-migration") {
		t.Errorf("review window missing: %v %q", err, out)
	}
}

// bin is the pr-owl binary under test, built once for the package.
var bin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pr-owl-e2e-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bin = filepath.Join(dir, "pr-owl")
	if out, err := exec.Command("go", "build", "-o", bin, "github.com/stefanahman/pr-owl").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "go build: %v\n%s", err, out)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// start runs pr-owl in the virtual terminal and makes sure it is gone
// when the test ends, however the test ends.
func start(t *testing.T, term *vttest.Terminal, dir string, env []string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Dir = dir
	cmd.Env = env
	if err := term.Start(cmd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

// waitExit waits for pr-owl to exit; a deadline turns a hang into a
// failure with the screen attached instead of a stuck run.
func waitExit(t *testing.T, term *vttest.Terminal, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- term.Wait(cmd) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pr-owl exited with %v:\n%s", err, screenText(term))
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("pr-owl did not exit within 20s:\n%s", screenText(term))
	}
}

// fixtureRepo is a clone whose origin is named like a GitHub repo (so
// currentRepo resolves acme/app) but fetches, through an insteadOf
// rule, from a local repository that has refs/pull/3543/head — so
// `open` works without any network.
func fixtureRepo(t *testing.T, root string) string {
	t.Helper()
	origin := filepath.Join(root, "origin")
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", origin},
		{"-C", origin, "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", origin, "commit", "-q", "--allow-empty", "-m", "PR 3543"},
		{"-C", origin, "update-ref", "refs/pull/3543/head", "HEAD"},
		{"init", "-q", "-b", "main", repo},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "init"},
		{"-C", repo, "remote", "add", "origin", "git@github.com:acme/app.git"},
		{"-C", repo, "config", "url." + origin + ".insteadOf", "git@github.com:acme/app.git"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = gitEnv(root)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

// gitEnv isolates git from the developer's configuration (signing,
// identity) for both the fixture and the binary under test. Built from
// scratch, not from os.Environ(): the developer's TERM/COLORTERM would
// change what the binary renders.
func gitEnv(root string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + root,
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=owl", "GIT_AUTHOR_EMAIL=owl@example.test",
		"GIT_COMMITTER_NAME=owl", "GIT_COMMITTER_EMAIL=owl@example.test",
	}
}

// fakeGH serves the three gh calls the TUI makes. The GraphQL fixture
// is generated with timestamps relative to now, so the rendered ages
// ("2h", "3d") don't drift as the calendar moves.
func fakeGH(t *testing.T, root string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	ago := func(d time.Duration) string { return time.Now().Add(-d).UTC().Format(time.RFC3339) }
	review := func(login, state, oid string, at time.Duration) map[string]any {
		return map[string]any{"author": map[string]string{"login": login}, "state": state, "submittedAt": ago(at), "commit": map[string]string{"oid": oid}}
	}
	pr := func(n int, title, author, oid string, at time.Duration, reviews ...map[string]any) map[string]any {
		if reviews == nil {
			reviews = []map[string]any{}
		}
		return map[string]any{
			"number": n, "title": title, "body": "Closes PROJ-" + fmt.Sprint(n), "url": fmt.Sprintf("https://github.com/acme/app/pull/%d", n),
			"headRefName": "feat/" + fmt.Sprint(n), "headRefOid": oid, "updatedAt": ago(at), "isDraft": false,
			"author": map[string]string{"login": author}, "reviews": map[string]any{"nodes": reviews},
		}
	}
	graphql := map[string]any{"data": map[string]any{
		"requested": map[string]any{"nodes": []any{
			pr(3543, "add billing migration", "alice", "aaa", 2*time.Hour),
			pr(3550, "fix retry ordering", "bob", "bbb", 5*time.Hour),
		}},
		"reviewed": map[string]any{"nodes": []any{
			pr(3491, "split ingestion worker", "carol", "ccc", 26*time.Hour, review("stefanahman", "COMMENTED", "old", 30*time.Hour)),
			pr(3502, "bump node to 22", "dave", "ddd", 3*24*time.Hour, review("stefanahman", "APPROVED", "ddd", 3*24*time.Hour)),
			pr(3510, "retry on 429", "erin", "eee", 8*time.Hour, review("stefanahman", "CHANGES_REQUESTED", "eee", 9*time.Hour)),
		}},
	}}
	data, err := json.Marshal(graphql)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "graphql.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
case "$1 $2" in
  "api graphql") cat "$(dirname "$0")/graphql.json" ;;
  "pr list") echo '[]' ;;
  "api user") echo stefanahman ;;
  "pr view") echo "Add Billing Migration" ;;
  *) echo "fake gh: unexpected $*" >&2; exit 1 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// hermeticEnv is the child's environment: fake gh first on PATH, no
// tmux server reachable, empty cache/config/Claude dirs, a config with
// one link, and a fixed terminal type so colours are stable.
func hermeticEnv(t *testing.T, root string) []string {
	t.Helper()
	cfg := filepath.Join(root, "config.yaml")
	config := "agent:\n  cmd: \"true\"\nlinks:\n  - key: l\n    name: Linear\n    pattern: 'PROJ-\\d+'\n    url: https://linear.app/acme/issue/{id}\n"
	if err := os.WriteFile(cfg, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	env := append(gitEnv(root)[1:], // PATH: the fake gh first
		"PATH="+path,
		// 256 colours, not truecolor: vttest compares snapshots after a
		// JSON round-trip, and only indexed colours survive it losslessly.
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"PR_OWL_CONFIG="+cfg,
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"),
		"TMUX_TMPDIR="+privateTmux(t, root),
		"TMUX=",
		"HISTFILE=",
	)
	return env
}

// privateTmux starts a tmux server of its own for the binary under
// test: /bin/sh windows, no exit when empty, its own short socket dir.
func privateTmux(t *testing.T, root string) string {
	t.Helper()
	sockDir, err := os.MkdirTemp("", "pr-owl-e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	conf := filepath.Join(sockDir, "tmux.conf")
	if err := os.WriteFile(conf, []byte("set -g default-shell /bin/sh\nset -s exit-empty off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	start := exec.Command("tmux", "-L", "default", "-f", conf, "start-server")
	start.Env = append(os.Environ(), "TMUX_TMPDIR="+sockDir, "TMUX=")
	if out, err := start.CombinedOutput(); err != nil {
		t.Fatalf("tmux start-server: %v\n%s", err, out)
	}
	socket := filepath.Join(sockDir, fmt.Sprintf("tmux-%d", os.Getuid()), "default")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	return sockDir
}

// waitFor polls the screen until want is visible.
func waitFor(t *testing.T, term *vttest.Terminal, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(screenText(term), want) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("screen never showed %q:\n%s", want, screenText(term))
}

func screenText(term *vttest.Terminal) string {
	var b strings.Builder
	for _, row := range term.Snapshot().Cells {
		for _, c := range row {
			b.WriteString(c.Content)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
