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
	"runtime"
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
// at github.com, a gh on PATH that serves fixture JSON, no tmux server,
// an empty Claude config dir, and a config with one link.
func TestScreen(t *testing.T) {
	root := t.TempDir()
	bin := buildBinary(t, root)
	repo := fixtureRepo(t, root)
	fakeGH(t, root)
	env := hermeticEnv(t, root)

	term, err := vttest.NewTerminal(t, 120, 30)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close()

	cmd := exec.Command(bin)
	cmd.Dir = repo
	cmd.Env = env
	if err := term.Start(cmd); err != nil {
		t.Fatal(err)
	}

	waitFor(t, term, "Waiting for author")
	snapshot.TestdataEqual(t, "list", term)

	term.SendKey(uv.KeyPressEvent{Code: '?', Text: "?"})
	waitFor(t, term, "pr-owl · help")
	snapshot.TestdataEqual(t, "help", term)

	term.SendKey(uv.KeyPressEvent{Code: 'q', Text: "q"})
	if err := term.Wait(cmd); err != nil {
		t.Fatalf("pr-owl exited with %v", err)
	}
}

// buildBinary compiles pr-owl once into root.
func buildBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(root, "pr-owl")
	out, err := exec.Command("go", "build", "-o", bin, "github.com/stefanahman/pr-owl").CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// fixtureRepo is a git repo with a GitHub-looking origin, so
// currentRepo resolves acme/app without any network.
func fixtureRepo(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "remote", "add", "origin", "git@github.com:acme/app.git"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
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
	link := "links:\n  - key: l\n    name: Linear\n    pattern: 'PROJ-\\d+'\n    url: https://linear.app/acme/issue/{id}\n"
	if err := os.WriteFile(cfg, []byte(link), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	env := []string{
		"PATH=" + path,
		"HOME=" + root,
		// 256 colours, not truecolor: vttest compares snapshots after a
		// JSON round-trip, and only indexed colours survive it losslessly.
		"TERM=xterm-256color",
		"LANG=en_US.UTF-8",
		"PR_OWL_CONFIG=" + cfg,
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(root, "claude"),
		"TMUX_TMPDIR=" + filepath.Join(root, "notmux"),
	}
	if runtime.GOOS == "darwin" {
		env = append(env, "TMPDIR="+root)
	}
	return env
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
