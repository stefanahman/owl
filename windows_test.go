package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stefanahman/mux"
	"github.com/stefanahman/mux/muxtest"
)

// TestMain lets the test binary stand in for the cmux CLI (see
// muxtest.FakeCmuxMain).
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "cmux" {
		os.Exit(muxtest.FakeCmuxMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestNewWindowsPicksTheMultiplexer(t *testing.T) {
	cfg := defaultConfig()
	cfg.Herdr.Socket = "/x/herdr.sock"
	t.Setenv("HERDR_ENV", "")
	t.Setenv("CMUX_WORKSPACE_ID", "")
	if k := newWindows(cfg, reviews).Kind(); k != "tmux" {
		t.Errorf("auto outside everything should be tmux, got %s", k)
	}
	t.Setenv("HERDR_ENV", "1")
	if k := newWindows(cfg, reviews).Kind(); k != "herdr" {
		t.Errorf("auto inside herdr should be herdr, got %s", k)
	}
	t.Setenv("HERDR_ENV", "")
	t.Setenv("CMUX_WORKSPACE_ID", "W1")
	if k := newWindows(cfg, reviews).Kind(); k != "cmux" {
		t.Errorf("auto inside cmux should be cmux, got %s", k)
	}
	cfg.Mux = "tmux"
	if k := newWindows(cfg, reviews).Kind(); k != "tmux" {
		t.Errorf("mux: tmux should win over the environment, got %s", k)
	}
	t.Setenv("CMUX_WORKSPACE_ID", "")
	cfg.Mux = "herdr"
	if h, ok := newWindows(cfg, reviews).d.(mux.Herdr); !ok || h.Socket != "/x/herdr.sock" {
		t.Errorf("mux: herdr should use the configured socket, got %#v", newWindows(cfg, reviews).d)
	}
	for _, k := range []string{"tmux", "herdr", "cmux"} {
		if w, ok := windowsByKind(k); !ok || w.Kind() != k {
			t.Errorf("windowsByKind(%s) = %v, %v", k, w, ok)
		}
	}
	if _, ok := windowsByKind(""); ok {
		t.Error("windowsByKind should know nothing else")
	}
	if got := (windows{mux.Tmux{SessionName: "s"}, reviews}).ChildEnv(); !reflect.DeepEqual(got, []string{"OWL_MUX=tmux"}) {
		t.Errorf("ChildEnv = %v", got)
	}
}

// The whole open/prompt/close flow on herdr: real git worktrees, the
// fake server in the multiplexer's seat, no tmux involved.
func TestOpenAndCloseOnHerdr(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	fake := muxtest.NewFakeHerdr(t)
	f.cfg.Mux = "herdr"
	f.cfg.Herdr.Socket = fake.Socket()
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("HERDR_SESSION", "work")
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": `echo "$OWL_MUX|$OWL_SESSION|$OWL_WINDOW" > ` + hookOut}

	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	out := f.open("42")
	if !strings.Contains(out, "started "+name+"\n") || strings.Contains(out, "attach with") {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "pr42.txt")) {
		t.Fatalf("worktree %s missing the PR's file", wt)
	}
	w := fake.Workspace(name)
	if w == nil || w.Cwd != wt || w.Focus {
		t.Fatalf("workspace = %+v, want cwd %s, created without focus", w, wt)
	}
	if fake.Focused() != w.ID {
		t.Errorf("arriving should have focused the workspace, focused = %q", fake.Focused())
	}
	if got, want := fake.Typed(w.Pane()), []string{"true '/owl:review 42'<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	f.waitFile(hookOut, "herdr|work|"+name+"\n")

	// The agent has exited (zsh in the foreground): a prompt restarts it.
	if out := f.open("42", "--prompt", "again"); !strings.Contains(out, "restarted agent in "+name) {
		t.Errorf("output: %q", out)
	}
	fake.SetForeground(w.Pane(), "claude")
	if out := f.open("42", "--prompt", "early"); !strings.Contains(out, "sent prompt to "+name) {
		t.Errorf("output: %q", out)
	}
	if got := fake.Typed(w.Pane()); got[len(got)-1] != "early<enter>" {
		t.Errorf("last typed %q, want the prompt typed while herdr has not detected the agent", got[len(got)-1])
	}
	fake.SetAgent(w.Pane(), "claude")
	if out := f.open("42", "--prompt", "look"); !strings.Contains(out, "sent prompt to "+name) {
		t.Errorf("output: %q", out)
	}
	if got := fake.Typed(w.Pane()); got[len(got)-1] != "prompt:look" {
		t.Errorf("last typed %q, want the prompt through agent.prompt", got[len(got)-1])
	}
	fake.SetStatus(name, "blocked")
	var buf strings.Builder
	if err := runOpen(f.cfg, []string{"42", "--prompt", "x"}, &buf, true); err == nil || !strings.Contains(err.Error(), "waiting for you in "+name) {
		t.Errorf("prompt while blocked: err = %v", err)
	}

	buf.Reset()
	if err := runClose(f.cfg, []string{"--force", "42"}, &buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "closed window "+name) || fake.Workspace(name) != nil || f.exists(wt) {
		t.Errorf("close: %q; workspace gone: %v; worktree gone: %v", buf.String(), fake.Workspace(name) == nil, !f.exists(wt))
	}
}

// The same flow on cmux: the fake CLI in the multiplexer's seat.
func TestOpenAndCloseOnCmux(t *testing.T) {
	f := newFixture(t)
	t.Chdir(f.repo)
	fake := muxtest.InstallFakeCmux(t)
	f.cfg.Mux = "cmux"
	hookOut := filepath.Join(f.root, "hook.out")
	f.cfg.Hooks.AfterOpen = hookByMux{"": `echo "$OWL_MUX|$OWL_SESSION|$OWL_WINDOW" > ` + hookOut}

	name := "pr-42-fix-crash-on-startup"
	wt := filepath.Join(f.repo, ".worktrees.local", name)
	out := f.open("42")
	if !strings.Contains(out, "started "+name+"\n") || strings.Contains(out, "attach with") {
		t.Errorf("output: %q", out)
	}
	if !f.exists(filepath.Join(wt, "pr42.txt")) {
		t.Fatalf("worktree %s missing the PR's file", wt)
	}
	w, ok := fake.Workspace(name)
	if !ok || w.Cwd != wt {
		t.Fatalf("workspace = %+v, %v; want cwd %s", w, ok, wt)
	}
	surface := w.Panes[0].Surfaces[0].ID
	if got, want := fake.Typed(surface), []string{"true '/owl:review 42'", "<enter>"}; !reflect.DeepEqual(got, want) {
		t.Errorf("typed %v, want %v", got, want)
	}
	if fake.State().Selected != w.ID {
		t.Errorf("arriving should have selected the workspace, selected = %q", fake.State().Selected)
	}
	f.waitFile(hookOut, "cmux||"+name+"\n")

	// The agent has exited (the shell alone in the foreground of the
	// surface's tty): a prompt restarts it.
	fake.SetForeground(w.Panes[0].Surfaces[0].TTY, "-/bin/zsh")
	if out := f.open("42", "--prompt", "again"); !strings.Contains(out, "restarted agent in "+name) {
		t.Errorf("output: %q", out)
	}
	fake.SetForeground(w.Panes[0].Surfaces[0].TTY, "-/bin/zsh", "/x/.local/bin/claude")
	if out := f.open("42", "--prompt", "look"); !strings.Contains(out, "sent prompt to "+name) {
		t.Errorf("output: %q", out)
	}
	if got := fake.Typed(surface); got[len(got)-2] != "look" {
		t.Errorf("typed %v, want the prompt typed last", got)
	}
	fake.AddSession(w.ID, "needsInput", true, "2026-09-08T16:00:00Z")
	var buf strings.Builder
	if err := runOpen(f.cfg, []string{"42", "--prompt", "x"}, &buf, true); err == nil || !strings.Contains(err.Error(), "waiting for you in "+name) {
		t.Errorf("prompt while blocked: err = %v", err)
	}

	buf.Reset()
	if err := runClose(f.cfg, []string{"--force", "42"}, &buf); err != nil {
		t.Fatal(err)
	}
	if _, still := fake.Workspace(name); !strings.Contains(buf.String(), "closed window "+name) || still || f.exists(wt) {
		t.Errorf("close: %q; workspace gone: %v; worktree gone: %v", buf.String(), !still, !f.exists(wt))
	}
}

func TestGlobalOptions(t *testing.T) {
	t.Cleanup(func() { configOverride, muxOverride = "", "" })
	rest, err := globalOptions([]string{"--config", "~/x.yaml", "--mux=cmux", "open", "42"})
	if err != nil || !reflect.DeepEqual(rest, []string{"open", "42"}) || configOverride != "~/x.yaml" || muxOverride != "cmux" {
		t.Errorf("rest %v, err %v, config %q, mux %q", rest, err, configOverride, muxOverride)
	}
	if p, _ := configPath(); !strings.HasSuffix(p, "/x.yaml") || strings.HasPrefix(p, "~") {
		t.Errorf("configPath with --config = %q", p)
	}
	// A child owl (open, close) gets the same options in front.
	if got := globalArgs(); !reflect.DeepEqual(got, []string{"--config", "~/x.yaml", "--mux", "cmux"}) {
		t.Errorf("globalArgs = %v", got)
	}
	configOverride, muxOverride = "", ""
	if got := globalArgs(); len(got) != 0 {
		t.Errorf("globalArgs without options = %v", got)
	}
	if rest, err := globalOptions([]string{"open", "--mux", "42"}); err != nil || len(rest) != 3 {
		t.Errorf("options after the command are the command's: %v, %v", rest, err)
	}
	for _, bad := range [][]string{{"--mux", "screen"}, {"--mux"}, {"--config"}} {
		if _, err := globalOptions(bad); err == nil {
			t.Errorf("%v: expected a usage error", bad)
		}
	}
}

func TestHookByMux(t *testing.T) {
	cases := []struct {
		yaml       string
		tmux, cmux string
	}{
		{`hooks: {after_open: ""}`, "", ""},
		{`hooks: {after_open: "spaces focus pr-reviews"}`, "spaces focus pr-reviews", "spaces focus pr-reviews"},
		{"hooks:\n  after_open: {tmux: spaces focus pr-reviews}", "spaces focus pr-reviews", ""},
		{"hooks:\n  after_open: {tmux: a, cmux: b}", "a", "b"},
	}
	for _, c := range cases {
		cfg, err := parseConfig([]byte(c.yaml))
		if err != nil {
			t.Fatalf("%s: %v", c.yaml, err)
		}
		if got := cfg.Hooks.AfterOpen.For("tmux"); got != c.tmux {
			t.Errorf("%s: tmux hook %q, want %q", c.yaml, got, c.tmux)
		}
		if got := cfg.Hooks.AfterOpen.For("cmux"); got != c.cmux {
			t.Errorf("%s: cmux hook %q, want %q", c.yaml, got, c.cmux)
		}
	}
	if _, err := parseConfig([]byte("hooks:\n  after_open: {screen: x}")); err == nil || !strings.Contains(err.Error(), "keys are multiplexers") {
		t.Errorf("unknown multiplexer key: %v", err)
	}
	if cfg, _ := parseConfig([]byte("hooks:\n  after_open: {tmux: ~/bin/focus}")); !strings.HasSuffix(cfg.Hooks.AfterOpen["tmux"], "/bin/focus") || strings.HasPrefix(cfg.Hooks.AfterOpen["tmux"], "~") {
		t.Errorf("~ not expanded: %q", cfg.Hooks.AfterOpen["tmux"])
	}
}
