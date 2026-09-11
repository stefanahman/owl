package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	if got := (windows{mux.Tmux{SessionName: "s"}, reviews, nil}).ChildEnv(); !reflect.DeepEqual(got, []string{"OWL_MUX=tmux"}) {
		t.Errorf("ChildEnv = %v", got)
	}
}

func TestGroupStyle(t *testing.T) {
	// Off by default, and nil is how Open knows not to group at all —
	// there is no style for a group that is never made.
	cfg := defaultConfig()
	for _, sc := range []scope{reviews, features, projects} {
		if got := groupStyle(cfg, sc); got != nil {
			t.Errorf("groupStyle(%s) = %+v with groups off, want nil", sc.name, got)
		}
	}
	if got := newWindows(cfg, features).style; got != nil {
		t.Errorf("newWindows carried a style with groups off: %+v", got)
	}

	// Switched on, one group per scope, named after the scope, so the
	// sidebar's words are the tmux sessions' words.
	cfg.Groups.Enabled = true
	for _, c := range []struct {
		sc          scope
		color, icon string
	}{
		{reviews, "#00afff", "eye"},
		{features, "#00d75f", "hammer"},
		{projects, "#af87ff", "square.stack.3d.up"},
	} {
		got := groupStyle(cfg, c.sc)
		if got == nil || got.Color != c.color || got.Icon != c.icon {
			t.Errorf("groupStyle(%s) = %+v, want %s %s", c.sc.name, got, c.color, c.icon)
		}
	}
	// The config overrides, and an empty value leaves the multiplexer's
	// own default rather than blanking it to something.
	cfg.Groups.Projects = GroupStyle{Color: "#ff0000"}
	if got := groupStyle(cfg, projects); got.Color != "#ff0000" || got.Icon != "" {
		t.Errorf("overridden projects group = %+v", got)
	}
	// The windows value carries it, so Open needs no new argument.
	if got := newWindows(cfg, features).style; got == nil || got.Icon != "hammer" {
		t.Errorf("newWindows did not carry the style: %+v", got)
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

// TestStatesInAsksAFlatMultiplexerOnce: the PR list reads two scopes
// now — a PR of yours opens into a feature's window — and the states of
// both used to mean two calls into the multiplexer. Only tmux keeps the
// scopes in separate sessions; cmux holds one flat list, where the
// second call fetched the same answer to filter differently. On cmux
// that is three subprocesses, every two seconds.
func TestStatesInAsksAFlatMultiplexerOnce(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho x >> " + calls + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "cmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := defaultConfig()
	cfg.Mux = "cmux"
	statesIn(cfg, nil, reviews, features)
	one, _ := os.ReadFile(calls)

	if err := os.Remove(calls); err != nil {
		t.Fatal(err)
	}
	statesIn(cfg, nil, reviews)
	alone, _ := os.ReadFile(calls)

	if got, want := len(strings.Split(string(one), "\n")), len(strings.Split(string(alone), "\n")); got != want {
		t.Errorf("two scopes cost %d calls into cmux, one scope %d — the flat list should be read once", got, want)
	}
}

// TestNoWatchKeepsNoDriver: a kept driver is only safe while a watch
// keeps it current.
//
// The cmux driver answers list and pills from a snapshot it fills on
// first read, and only a mutating call ever clears it. With a live
// watch that snapshot is bypassed entirely — States() answers from the
// watcher's view — but without one, a driver held across reads would
// report the states it saw first for as long as the list ran. So when
// the watch fails to start, the driver goes with it and reads go back
// to a fresh driver each time.
func TestNoWatchKeepsNoDriver(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A cmux that is not there: Watch's first read fails, so no watch.
	if err := os.WriteFile(filepath.Join(bin, "cmux"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := defaultConfig()
	cfg.Mux = "cmux"
	msg, ok := startWatch(context.Background(), cfg)().(watchMsg)
	if !ok {
		t.Fatalf("startWatch returned %T", startWatch(context.Background(), cfg)())
	}
	if msg.signal != nil {
		t.Error("a watch was reported where cmux does not answer")
	}
	if msg.driver != nil {
		t.Error("the driver was kept without a watch to keep it current; its snapshot would freeze")
	}
}

// TestTickCadenceFollowsTheWatch: the timer is two different things.
//
// Under cmux a watch reports a state the instant it changes and the
// timer is only a net for what a watch cannot see, so it can be slow.
// Under tmux and herdr there is no watch and the timer is still the
// only way a © ever changes colour — slowing it there would have
// traded a real cost for a regression nobody asked for.
func TestTickCadenceFollowsTheWatch(t *testing.T) {
	plain, watched := localRefreshInterval(false), localRefreshInterval(true)
	if plain != localRefreshEvery || watched != localRefreshWatched {
		t.Errorf("intervals = %v without a watch, %v with; want %v and %v",
			plain, watched, localRefreshEvery, localRefreshWatched)
	}
	if plain >= watched {
		t.Errorf("the unwatched tick (%v) is not the faster of the two (%v)", plain, watched)
	}
}

// TestWatchEndsWithTheList: cmux's event stream is a child process
// held open for as long as the list watches, and mux kills it when the
// context it was started on is cancelled. Starting it on a context
// that is never cancelled would leave the child to notice the broken
// pipe on its next heartbeat — so the list's own context is what it
// gets, and cancelling that must close the channel.
func TestWatchEndsWithTheList(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	// A cmux that answers the first read and then streams nothing.
	script := "#!/bin/sh\ncase \"$1 $2\" in\n  'workspace list') echo '{\"workspaces\":[]}' ;;\n  'events') sleep 60 ;;\n  *) echo '{}' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "cmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := defaultConfig()
	cfg.Mux = "cmux"
	ctx, stop := context.WithCancel(context.Background())
	msg, ok := startWatch(ctx, cfg)().(watchMsg)
	if !ok || msg.signal == nil {
		stop()
		t.Skip("this cmux did not yield a watch; the lifetime is what is under test, not the fake")
	}

	stop()
	select {
	case _, open := <-msg.signal:
		if open {
			t.Error("cancelling the list's context left the watch signalling")
		}
	case <-time.After(5 * time.Second):
		t.Error("the watch outlived the context it was started on")
	}
}
