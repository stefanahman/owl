package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The template is the user-facing documentation of the defaults, so
// the two must never drift apart.
func TestTemplateMatchesDefaults(t *testing.T) {
	got, err := parseConfig([]byte(configTemplate))
	if err != nil {
		t.Fatalf("template does not parse: %v", err)
	}
	if want := defaultConfig(); !reflect.DeepEqual(got, want) {
		t.Errorf("template parses to\n%+v\nwant defaults\n%+v", got, want)
	}
}

func TestParseConfigOverlaysDefaults(t *testing.T) {
	cfg, err := parseConfig([]byte(`
tmux:
  session: reviews
theme:
  done: "#00ff00"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tmux.Session != "reviews" {
		t.Errorf("tmux.session = %q, want reviews", cfg.Tmux.Session)
	}
	if cfg.Tmux.KeepaliveWindow != "scratch" {
		t.Errorf("tmux.keepalive_window = %q, want default scratch", cfg.Tmux.KeepaliveWindow)
	}
	if cfg.Theme.Done != "#00ff00" || cfg.Theme.Working != "#dbbc7f" {
		t.Errorf("theme = %+v, want done overridden and working default", cfg.Theme)
	}
	if got := len(cfg.Agent.LinkLocal); got != 3 {
		t.Errorf("agent.link_local has %d entries, want the 3 defaults", got)
	}
}

func TestBindings(t *testing.T) {
	cfg, err := parseConfig([]byte(`
bindings:
  pr:
    - key: d
      name: Dependabot
      prompt: "/owl:dependabot {pr}"
    - key: l
      name: Linear
      pattern: 'PROJ-\d+'
      url: https://tracker.example/{id}
    - key: [b, B]
      name: CI
      url: https://ci.example/{repo}/{pr}/{branch}?from={url}
  issue:
    - key: p
      name: Continue
      prompt: "Continue {key} on {branch}"
      when: conversation
    - key: l
      name: Ticket
      url: "{url}"
`))
	if err != nil {
		t.Fatal(err)
	}
	// The shipped f comes first, the user's follow in their order.
	var names []string
	for _, b := range cfg.Bindings.PR {
		names = append(names, b.Name)
	}
	if want := []string{"check feedback", "Dependabot", "Linear", "CI"}; !reflect.DeepEqual(names, want) {
		t.Errorf("pr bindings = %v, want %v", names, want)
	}
	if f := cfg.Bindings.PR[0]; f.Key[0] != "f" || f.When != whenConversation || f.Prompt != feedbackPrompt {
		t.Errorf("shipped f = %+v", f)
	}
	pr := &PR{Number: 42, Title: "fix PROJ-7 crash", HeadRefName: "fix/crash", URL: "https://gh/x/pull/42"}
	if text, ok := cfg.Bindings.PR[1].forPR("acme/app", pr); !ok || text != "/owl:dependabot 42" {
		t.Errorf("dependabot prompt: %q, %v", text, ok)
	}
	if url, ok := cfg.Bindings.PR[2].forPR("acme/app", pr); !ok || url != "https://tracker.example/PROJ-7" {
		t.Errorf("linear link: got %q, %v", url, ok)
	}
	if _, ok := cfg.Bindings.PR[2].forPR("acme/app", &PR{Title: "no ticket"}); ok {
		t.Error("a binding with a pattern should be a no-op when nothing matches")
	}
	if url, ok := cfg.Bindings.PR[3].forPR("acme/app", pr); !ok || url != "https://ci.example/acme/app/42/fix/crash?from=https://gh/x/pull/42" {
		t.Errorf("ci link: got %q, %v", url, ok)
	}
	// By key, not by index: the shipped bindings come first, so an
	// index here would move every time owl ships another one.
	byKey := func(list []Binding, key string) Binding {
		for _, b := range list {
			if len(b.Key) > 0 && b.Key[0] == key {
				return b
			}
		}
		t.Fatalf("no binding on %q in %+v", key, list)
		return Binding{}
	}
	is := &Issue{Key: "BAR-9", Title: "Fix the owl", Branch: "bar-9-fix-the-owl", URL: "https://linear.app/x/issue/BAR-9"}
	if text, ok := byKey(cfg.Bindings.Issue, "p").forIssue("acme/app", is); !ok || text != "Continue BAR-9 on bar-9-fix-the-owl" {
		t.Errorf("issue prompt: %q, %v", text, ok)
	}
	if url, ok := byKey(cfg.Bindings.Issue, "l").forIssue("acme/app", is); !ok || url != is.URL {
		t.Errorf("issue url: %q, %v", url, ok)
	}
	// The shipped review key is there, on both lists, and only where a
	// conversation exists to send it to.
	review := byKey(cfg.Bindings.Issue, "f")
	if review.When != whenConversation || !strings.Contains(review.Prompt, "failure mode") {
		t.Errorf("shipped issue f = %+v", review)
	}

	// A user's f replaces the shipped one, in place.
	cfg, err = parseConfig([]byte("bindings:\n  pr:\n    - key: f\n      name: mine\n      prompt: again\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bindings.PR) != 1 || cfg.Bindings.PR[0].Name != "mine" || cfg.Bindings.PR[0].When != "" {
		t.Errorf("f replaced: %+v", cfg.Bindings.PR)
	}
	// The same key may mean different things on the two lists.
	if _, err := parseConfig([]byte("bindings:\n  pr:\n    - {key: x, name: a, url: https://a}\n  issue:\n    - {key: x, name: b, url: https://b}\n")); err != nil {
		t.Errorf("one key on both lists: %v", err)
	}
}

func TestParseConfigSaysWhereOldKeysMoved(t *testing.T) {
	for data, want := range map[string]string{
		"links:\n  - key: l\n    name: x\n    url: https://x\n": "links moved to bindings.pr",
		"keys:\n  feedback: f\n":                                "keys.feedback moved to bindings.pr",
		"agent:\n  feedback_prompt: x\n":                        "agent.feedback_prompt moved to bindings.pr",
	} {
		_, err := parseConfig([]byte(data))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", data, err, want)
		}
	}
}

func TestParseConfigKeys(t *testing.T) {
	cfg, err := parseConfig([]byte("keys:\n  quit: x\n  yank: [Y, ctrl+y]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Keys.Quit, keyNames{"x"}) {
		t.Errorf("scalar key: got %v, want [x]", cfg.Keys.Quit)
	}
	if !reflect.DeepEqual(cfg.Keys.Yank, keyNames{"Y", "ctrl+y"}) {
		t.Errorf("list key: got %v, want [Y ctrl+y]", cfg.Keys.Yank)
	}
	if !reflect.DeepEqual(cfg.Keys.Up, keyNames{"up", "k"}) {
		t.Errorf("untouched action lost its default: %v", cfg.Keys.Up)
	}
}

func TestKeyLabel(t *testing.T) {
	cases := map[string]keyNames{
		"↑/k":  {"up", "k"},
		"↵":    {"enter"},
		"q":    {"q", "ctrl+c"},
		"pgdn": {"pgdown", "ctrl+d"},
		"g":    {"g", "home"},
		"l/L":  {"l", "L"},
	}
	for want, keys := range cases {
		if got := keys.label(); got != want {
			t.Errorf("%v.label() = %q, want %q", keys, got, want)
		}
	}
}

func TestParseConfigEmptyIsDefaults(t *testing.T) {
	for _, data := range []string{"", "\n", "# only a comment\n"} {
		cfg, err := parseConfig([]byte(data))
		if err != nil {
			t.Fatalf("%q: %v", data, err)
		}
		if !reflect.DeepEqual(cfg, defaultConfig()) {
			t.Errorf("%q: got %+v, want defaults", data, cfg)
		}
	}
}

func TestParseConfigRejects(t *testing.T) {
	cases := map[string]string{
		"unknown key":            "tmux:\n  sesion: x\n",
		"empty session":          "tmux:\n  session: ''\n",
		"absolute worktrees":     "worktrees_dir: /tmp/wt\n",
		"repo root worktrees":    "worktrees_dir: .\n",
		"parent worktrees":       "worktrees_dir: ../wt\n",
		"hidden parent":          "worktrees_dir: wt/../../x\n",
		"bad colour":             "theme:\n  done: green\n",
		"colour out of range":    "theme:\n  done: \"256\"\n",
		"bad on_open":            "on_open: close\n",
		"not a mapping":          "- a\n- b\n",
		"unbound action":         "keys:\n  quit: []\n",
		"empty key name":         "keys:\n  quit: ''\n",
		"key bound twice":        "keys:\n  quit: r\n",
		"binding without name":   "bindings:\n  pr:\n    - key: l\n      url: https://x\n",
		"binding without action": "bindings:\n  pr:\n    - key: l\n      name: x\n",
		"binding both actions":   "bindings:\n  pr:\n    - key: l\n      name: x\n      url: https://x\n      prompt: y\n",
		"binding without key":    "bindings:\n  pr:\n    - name: x\n      url: https://x\n",
		"binding bad regexp":     "bindings:\n  pr:\n    - key: l\n      name: x\n      pattern: '('\n      url: https://x/{id}\n",
		"binding id no pattern":  "bindings:\n  pr:\n    - key: l\n      name: x\n      prompt: \"see {id}\"\n",
		"binding bad when":       "bindings:\n  pr:\n    - key: l\n      name: x\n      prompt: y\n      when: always\n",
		"binding key collides":   "bindings:\n  pr:\n    - key: q\n      name: x\n      url: https://x\n",
		"bindings collide":       "bindings:\n  pr:\n    - {key: l, name: a, url: https://a}\n    - {key: l, name: b, url: https://b}\n",
		"issue binding collides": "bindings:\n  issue:\n    - {key: s, name: a, url: https://a}\n",
	}
	for name, data := range cases {
		if _, err := parseConfig([]byte(data)); err == nil {
			t.Errorf("%s: expected an error for %q", name, data)
		}
	}
	if _, err := parseConfig([]byte("worktrees_dir: ..hidden\n")); err != nil {
		t.Errorf("..hidden is a valid directory name: %v", err)
	}
}

func TestParseConfigExpandsHome(t *testing.T) {
	t.Setenv("HOME", "/home/owl")
	cfg, err := parseConfig([]byte("default_repo: ~/src/app\nhooks:\n  after_open: ~/bin/focus\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultRepo != "/home/owl/src/app" || cfg.Hooks.AfterOpen.For("tmux") != "/home/owl/bin/focus" {
		t.Errorf("got default_repo=%q after_open=%q", cfg.DefaultRepo, cfg.Hooks.AfterOpen)
	}
}

func TestConfigPath(t *testing.T) {
	t.Setenv("HOME", "/home/owl")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("OWL_CONFIG", "")
	assertPath := func(want string) {
		t.Helper()
		got, err := configPath()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("configPath() = %q, want %q", got, want)
		}
	}
	assertPath("/home/owl/.config/owl/config.yaml")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	assertPath("/xdg/owl/config.yaml")
	t.Setenv("OWL_CONFIG", "/explicit.yaml")
	assertPath("/explicit.yaml")
}

func TestLoadConfigMissingFileIsDefaults(t *testing.T) {
	t.Setenv("OWL_CONFIG", filepath.Join(t.TempDir(), "nope.yaml"))
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, defaultConfig()) {
		t.Errorf("got %+v, want defaults", cfg)
	}
}

func TestLoadConfigNamesFileInErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("bogus: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWL_CONFIG", path)
	_, err := loadConfig()
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Errorf("error %v should name %s", err, path)
	}
}

func TestConfigInit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	t.Setenv("OWL_CONFIG", path)
	var out strings.Builder
	if err := runConfig([]string{"init"}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != path+"\n" {
		t.Errorf("init printed %q, want the path", out.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != configTemplate {
		t.Error("init did not write the template")
	}
	if err := runConfig([]string{"init"}, io.Discard); err == nil {
		t.Error("second init should refuse to overwrite")
	}
}

func TestRunConfigUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"get"}, {"get", "a", "b"}} {
		err := runConfig(args, io.Discard)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%v: got %v, want a usageError", args, err)
		}
	}
}

// TestOnOpenDefaultsToAuto: the default follows the multiplexer (see
// TestOnOpenAutoFollowsTheMultiplexer); the four words parse, others
// don't.
func TestOnOpenDefaultsToAuto(t *testing.T) {
	if got := defaultConfig().OnOpen; got != "auto" {
		t.Errorf("default on_open = %q, want auto", got)
	}
	for _, word := range []string{"auto", "quit", "stay", "switch"} {
		if cfg, err := parseConfig([]byte("on_open: " + word + "\n")); err != nil || cfg.OnOpen != word {
			t.Errorf("on_open: %s: %v", word, err)
		}
	}
}
