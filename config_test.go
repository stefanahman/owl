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

func TestLinks(t *testing.T) {
	cfg, err := parseConfig([]byte(`
links:
  - key: l
    name: Linear
    pattern: 'PROJ-\d+'
    url: https://tracker.example/{id}
  - key: [b, B]
    name: CI
    url: https://ci.example/{repo}/{pr}/{branch}?from={url}
`))
	if err != nil {
		t.Fatal(err)
	}
	pr := &PR{Number: 42, Title: "fix PROJ-7 crash", HeadRefName: "fix/crash", URL: "https://gh/x/pull/42"}

	url, ok := cfg.Links[0].expand("acme/app", pr)
	if !ok || url != "https://tracker.example/PROJ-7" {
		t.Errorf("linear link: got %q, %v", url, ok)
	}
	if _, ok := cfg.Links[0].expand("acme/app", &PR{Title: "no ticket"}); ok {
		t.Error("link with a pattern should be a no-op when nothing matches")
	}
	url, ok = cfg.Links[1].expand("acme/app", pr)
	if !ok || url != "https://ci.example/acme/app/42/fix/crash?from=https://gh/x/pull/42" {
		t.Errorf("ci link: got %q, %v", url, ok)
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
		"unknown key":         "tmux:\n  sesion: x\n",
		"empty session":       "tmux:\n  session: ''\n",
		"absolute worktrees":  "worktrees_dir: /tmp/wt\n",
		"repo root worktrees": "worktrees_dir: .\n",
		"parent worktrees":    "worktrees_dir: ../wt\n",
		"hidden parent":       "worktrees_dir: wt/../../x\n",
		"bad colour":          "theme:\n  done: green\n",
		"colour out of range": "theme:\n  done: \"256\"\n",
		"bad on_open":         "on_open: close\n",
		"not a mapping":       "- a\n- b\n",
		"unbound action":      "keys:\n  quit: []\n",
		"empty key name":      "keys:\n  quit: ''\n",
		"key bound twice":     "keys:\n  quit: r\n",
		"link without name":   "links:\n  - key: l\n    url: https://x\n",
		"link without url":    "links:\n  - key: l\n    name: x\n",
		"link without key":    "links:\n  - name: x\n    url: https://x\n",
		"link bad regexp":     "links:\n  - key: l\n    name: x\n    pattern: '('\n    url: https://x/{id}\n",
		"link id no pattern":  "links:\n  - key: l\n    name: x\n    url: https://x/{id}\n",
		"link key collides":   "links:\n  - key: q\n    name: x\n    url: https://x\n",
		"links collide":       "links:\n  - {key: l, name: a, url: https://a}\n  - {key: l, name: b, url: https://b}\n",
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
