// Configuration: $XDG_CONFIG_HOME/owl/config.yaml, or the file named
// by --config or $OWL_CONFIG.
// Every key has a default; a missing file is not an error. Keys the
// file doesn't mention keep their defaults, unknown keys are rejected
// so a typo can't silently fall back to the default.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is the parsed config file; see configTemplate for what each
// key means. Obtain one via loadConfig (or defaultConfig in tests) so
// it has been validated and the derived fields are set.
type Config struct {
	Mux          string       `yaml:"mux"`
	Tmux         TmuxConfig   `yaml:"tmux"`
	Herdr        HerdrConfig  `yaml:"herdr"`
	Remote       string       `yaml:"remote"`
	WorktreesDir string       `yaml:"worktrees_dir"`
	DefaultRepo  string       `yaml:"default_repo"`
	Agent        AgentConfig  `yaml:"agent"`
	OpenCmd      string       `yaml:"open_cmd"`
	OnOpen       string       `yaml:"on_open"`
	Hooks        HooksConfig  `yaml:"hooks"`
	Theme        ThemeConfig  `yaml:"theme"`
	Keys         KeysConfig   `yaml:"keys"`
	Links        []LinkConfig `yaml:"links"`
}

type TmuxConfig struct {
	Session         string `yaml:"session"`
	KeepaliveWindow string `yaml:"keepalive_window"`
}

type HerdrConfig struct {
	Socket string `yaml:"socket"`
}

type AgentConfig struct {
	Cmd            string   `yaml:"cmd"`
	Prompt         string   `yaml:"prompt"`
	FeedbackPrompt string   `yaml:"feedback_prompt"`
	LinkLocal      []string `yaml:"link_local"`
}

// LinkConfig is a user-defined key that opens a URL built from the
// selected PR. URL placeholders: {pr}, {repo}, {branch}, {url}, and
// {id} — the first match of Pattern in the PR's title, body and branch.
type LinkConfig struct {
	Key     keyNames `yaml:"key"`
	Name    string   `yaml:"name"` // shown in the help view
	Pattern string   `yaml:"pattern"`
	URL     string   `yaml:"url"`

	re *regexp.Regexp // compiled Pattern; nil when Pattern is empty
}

// expand builds the link's URL for a PR. ok is false when Pattern is
// set and matches nothing — the key then does nothing.
func (l LinkConfig) expand(repo string, pr *PR) (url string, ok bool) {
	id := ""
	if l.re != nil {
		id = l.re.FindString(pr.Title + "\n" + pr.Body + "\n" + pr.HeadRefName)
		if id == "" {
			return "", false
		}
	}
	r := strings.NewReplacer(
		"{pr}", strconv.Itoa(pr.Number), "{repo}", repo, "{branch}", pr.HeadRefName, "{url}", pr.URL, "{id}", id,
	)
	return r.Replace(l.URL), true
}

type HooksConfig struct {
	AfterOpen hookByMux `yaml:"after_open"`
}

// hookByMux is a hook command, one for every multiplexer (a string)
// or one per multiplexer (a mapping keyed tmux, herdr, cmux): a hook
// that focuses a tmux session has no meaning under herdr or cmux.
type hookByMux map[string]string

func (h *hookByMux) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		if s == "" {
			*h = nil
		} else {
			*h = hookByMux{"": s}
		}
		return nil
	}
	var m map[string]string
	if err := n.Decode(&m); err != nil {
		return err
	}
	*h = m
	return nil
}

// For is the hook for a multiplexer: its own, else the one for all.
func (h hookByMux) For(kind string) string {
	if cmd, ok := h[kind]; ok {
		return cmd
	}
	return h[""]
}

type ThemeConfig struct {
	Working string `yaml:"working"`
	Blocked string `yaml:"blocked"`
	Done    string `yaml:"done"`
}

// KeysConfig binds the TUI actions. Key names are bubbletea's
// (`enter`, `esc`, `pgup`, `ctrl+u`, single characters, ...).
type KeysConfig struct {
	Up       keyNames `yaml:"up"`
	Down     keyNames `yaml:"down"`
	Top      keyNames `yaml:"top"`
	Bottom   keyNames `yaml:"bottom"`
	PageUp   keyNames `yaml:"page_up"`
	PageDown keyNames `yaml:"page_down"`
	Open     keyNames `yaml:"open"`
	Start    keyNames `yaml:"start"`
	Feedback keyNames `yaml:"feedback"`
	Browser  keyNames `yaml:"browser"`
	Yank     keyNames `yaml:"yank"`
	Next     keyNames `yaml:"next"`
	Cleanup  keyNames `yaml:"cleanup"`
	Search   keyNames `yaml:"search"`
	Cancel   keyNames `yaml:"cancel"`
	Refresh  keyNames `yaml:"refresh"`
	Help     keyNames `yaml:"help"`
	Quit     keyNames `yaml:"quit"`
}

// each visits every action with its config name, in help order.
func (k KeysConfig) each(fn func(action string, keys keyNames)) {
	fn("up", k.Up)
	fn("down", k.Down)
	fn("top", k.Top)
	fn("bottom", k.Bottom)
	fn("page_up", k.PageUp)
	fn("page_down", k.PageDown)
	fn("open", k.Open)
	fn("start", k.Start)
	fn("feedback", k.Feedback)
	fn("browser", k.Browser)
	fn("yank", k.Yank)
	fn("next", k.Next)
	fn("cleanup", k.Cleanup)
	fn("search", k.Search)
	fn("cancel", k.Cancel)
	fn("refresh", k.Refresh)
	fn("help", k.Help)
	fn("quit", k.Quit)
}

// validateBindings rejects actions and links with no key, and a key
// bound twice (built-in action or link).
func validateBindings(k KeysConfig, links []LinkConfig) error {
	var errs []error
	bound := map[string]string{} // key name → "keys.quit" / "links[0]"
	claim := func(owner string, keys keyNames) {
		if len(keys) == 0 {
			errs = append(errs, fmt.Errorf("%s: no key", owner))
		}
		for _, key := range keys {
			if key == "" {
				errs = append(errs, fmt.Errorf("%s: empty key name", owner))
				continue
			}
			if other, dup := bound[key]; dup {
				errs = append(errs, fmt.Errorf("key %q is bound to both %s and %s", key, other, owner))
				continue
			}
			bound[key] = owner
		}
	}
	k.each(func(action string, keys keyNames) { claim("keys."+action, keys) })
	for i, l := range links {
		claim(fmt.Sprintf("links[%d]", i), l.Key)
	}
	return errors.Join(errs...)
}

// keyNames is one key name or a list of them: `quit: q` or
// `quit: [q, ctrl+c]`.
type keyNames []string

func (k *keyNames) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var s string
		if err := n.Decode(&s); err != nil {
			return err
		}
		*k = keyNames{s}
		return nil
	}
	var list []string
	if err := n.Decode(&list); err != nil {
		return err
	}
	*k = list
	return nil
}

// configTemplate is what `owl config init` writes. It is the
// documentation for every key, and TestTemplateMatchesDefaults keeps it
// equal to defaultConfig().
const configTemplate = `# owl configuration. Every key is optional; these are the defaults.

mux: auto                        # the multiplexer reviews run in: tmux, herdr, cmux, or auto (herdr or cmux when owl runs inside one, else tmux)

tmux:
  session: reviews               # session that holds one window per review
  keepalive_window: scratch      # window that keeps the session alive with no reviews open

herdr:
  socket: ""                     # herdr's socket; default: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock

remote: origin                   # git remote of the GitHub repo: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # where review worktrees go, relative to the repo root (added to .git/info/exclude)
default_repo: ""                 # repo to use when owl is started outside a git repo; ~ is expanded

agent:
  cmd: claude --permission-mode auto     # Claude Code, with your flags (e.g. --model claude-opus-5); owl appends -c when the worktree has a prior conversation (found in ~/.claude/projects)
  prompt: "/pr-review:pr-review {pr}"    # first prompt of a fresh review; {pr} is the PR number
  feedback_prompt: "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict."
  link_local:                            # globs relative to the repo root, symlinked into each new worktree
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: quit                    # the TUI once an open starts: quit (a popup closes at once; open finishes behind it), stay (keep the list), switch (move your client to the reviews, for owl in a tmux window; under herdr the focus has already moved)

hooks:
  after_open: ""                 # command run after ` + "`owl open`" + ` with OWL_PR, OWL_WINDOW, OWL_WORKTREE, OWL_REPO, OWL_MUX set, and OWL_SESSION under tmux and herdr; ~ is expanded.
                                 # A mapping gives one per multiplexer, e.g. {tmux: spaces focus pr-reviews}: none under herdr and cmux, where the window is already in front

theme:                           # lipgloss colours: ANSI 0-255 or #rrggbb
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # one key name or a list; names as bubbletea spells them (enter, esc, pgup, ctrl+u, ...)
  up: [up, k]
  down: [down, j]
  top: [g, home]
  bottom: [G, end]
  page_up: [pgup, ctrl+u]
  page_down: [pgdown, ctrl+d]
  open: enter
  start: s
  feedback: f
  browser: o
  yank: y
  next: n
  cleanup: c
  search: /
  cancel: esc
  refresh: r
  help: "?"
  quit: [q, ctrl+c]

# links: extra keys, each opening a URL built from the selected PR (via open_cmd).
# Placeholders: {pr} number, {repo} owner/name, {branch} head branch, {url} the PR's page,
# and {id} — the first match of ` + "`pattern`" + ` in the PR title, body and branch (no match → the key does nothing).
#
# links:
#   - key: l
#     name: Linear                # shown in the help view
#     pattern: 'PROJ-\d+'
#     url: https://linear.app/<org>/issue/{id}
`

func defaultConfig() Config {
	var c Config
	c.Mux = "auto"
	c.Tmux = TmuxConfig{Session: "reviews", KeepaliveWindow: "scratch"}
	c.Remote = "origin"
	c.WorktreesDir = ".worktrees.local"
	c.Agent = AgentConfig{
		Cmd:    "claude --permission-mode auto",
		Prompt: "/pr-review:pr-review {pr}",
		// The `f` key's message. Two-pass self-critique + a RESOLVED
		// taxonomy, calm tenor: encouraging language increases
		// deliberation, urgency causes shortcuts.
		FeedbackPrompt: "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict.",
		LinkLocal:      []string{".claude/settings.local.json", ".claude/*.local.md", ".claude/skills/*.local"},
	}
	c.OnOpen = "quit"
	c.Theme = ThemeConfig{Working: "#dbbc7f", Blocked: "214", Done: "42"}
	c.Keys = KeysConfig{
		Up: keyNames{"up", "k"}, Down: keyNames{"down", "j"},
		Top: keyNames{"g", "home"}, Bottom: keyNames{"G", "end"},
		PageUp: keyNames{"pgup", "ctrl+u"}, PageDown: keyNames{"pgdown", "ctrl+d"},
		Open: keyNames{"enter"}, Start: keyNames{"s"}, Feedback: keyNames{"f"}, Browser: keyNames{"o"},
		Yank: keyNames{"y"}, Next: keyNames{"n"}, Cleanup: keyNames{"c"},
		Search: keyNames{"/"}, Cancel: keyNames{"esc"}, Refresh: keyNames{"r"}, Help: keyNames{"?"},
		Quit: keyNames{"q", "ctrl+c"},
	}
	return c
}

// configOverride is the --config flag, when given.
var configOverride string

// configPath is --config, else $OWL_CONFIG, else
// $XDG_CONFIG_HOME/owl/config.yaml, else ~/.config/owl/config.yaml.
func configPath() (string, error) {
	if configOverride != "" {
		return expandHome(configOverride), nil
	}
	if p := os.Getenv("OWL_CONFIG"); p != "" {
		return p, nil
	}
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "owl", "config.yaml"), nil
}

// loadConfig returns the defaults overlaid with the config file, if any.
func loadConfig() (Config, error) {
	path, err := configPath()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return parseConfig(nil)
	}
	if err != nil {
		return Config{}, err
	}
	cfg, err := parseConfig(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// parseConfig overlays YAML onto the defaults and validates the result.
func parseConfig(data []byte) (Config, error) {
	cfg := defaultConfig()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF: no document at all (empty file, comments only).
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	cfg.DefaultRepo = expandHome(cfg.DefaultRepo)
	for k, v := range cfg.Hooks.AfterOpen {
		cfg.Hooks.AfterOpen[k] = expandHome(v)
	}
	cfg.Herdr.Socket = expandHome(cfg.Herdr.Socket)
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// validate checks the overlaid config and compiles the link patterns.
func (cfg *Config) validate() error {
	required := []struct{ name, value string }{
		{"tmux.session", cfg.Tmux.Session},
		{"tmux.keepalive_window", cfg.Tmux.KeepaliveWindow},
		{"remote", cfg.Remote},
		{"worktrees_dir", cfg.WorktreesDir},
		{"agent.cmd", cfg.Agent.Cmd},
		{"agent.prompt", cfg.Agent.Prompt},
		{"agent.feedback_prompt", cfg.Agent.FeedbackPrompt},
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("%s must not be empty", r.name)
		}
	}
	if d := filepath.Clean(cfg.WorktreesDir); filepath.IsAbs(d) || d == "." || d == ".." || strings.HasPrefix(d, "../") {
		return fmt.Errorf("worktrees_dir must be a relative path inside the repo, got %q", cfg.WorktreesDir)
	}
	switch cfg.OnOpen {
	case "quit", "stay", "switch":
	default:
		return fmt.Errorf("on_open must be quit, stay or switch, got %q", cfg.OnOpen)
	}
	switch cfg.Mux {
	case "auto", "tmux", "herdr", "cmux":
	default:
		return fmt.Errorf("mux must be auto, tmux, herdr or cmux, got %q", cfg.Mux)
	}
	for k := range cfg.Hooks.AfterOpen {
		switch k {
		case "", "tmux", "herdr", "cmux":
		default:
			return fmt.Errorf("hooks.after_open: keys are multiplexers (tmux, herdr, cmux), got %q", k)
		}
	}
	for _, c := range []struct{ name, value string }{
		{"theme.working", cfg.Theme.Working}, {"theme.blocked", cfg.Theme.Blocked}, {"theme.done", cfg.Theme.Done},
	} {
		if !validColor(c.value) {
			return fmt.Errorf("%s: %q is not an ANSI colour number (0-255) or #rrggbb", c.name, c.value)
		}
	}
	for i := range cfg.Links {
		l := &cfg.Links[i]
		if l.Name == "" || l.URL == "" {
			return fmt.Errorf("links[%d]: name and url are required", i)
		}
		if l.Pattern != "" {
			re, err := regexp.Compile(l.Pattern)
			if err != nil {
				return fmt.Errorf("links[%d].pattern: %w", i, err)
			}
			l.re = re
		} else if strings.Contains(l.URL, "{id}") {
			return fmt.Errorf("links[%d]: url uses {id} but no pattern is set", i)
		}
	}
	return validateBindings(cfg.Keys, cfg.Links)
}

var colorRe = regexp.MustCompile(`^(#[0-9a-fA-F]{6}|[0-9]{1,3})$`)

// validColor accepts what lipgloss.Color renders: 0-255 or #rrggbb.
func validColor(s string) bool {
	if !colorRe.MatchString(s) {
		return false
	}
	if s[0] == '#' {
		return true
	}
	n, _ := strconv.Atoi(s)
	return n <= 255
}

// expandHome turns a leading ~ into the home directory. YAML has no
// shell, so users writing `~/.local/bin/...` expect this.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + p[1:]
		}
	}
	return p
}

// runConfig implements `owl config init | path`, writing results
// to w.
func runConfig(args []string, w io.Writer) error {
	if len(args) == 0 {
		return usageError("config: expected init or path")
	}
	switch args[0] {
	case "path":
		path, err := configPath()
		if err != nil {
			return err
		}
		fmt.Fprintln(w, path)
		return nil
	case "init":
		path, err := configPath()
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists", path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(configTemplate), 0o644); err != nil {
			return err
		}
		fmt.Fprintln(w, path)
		return nil
	}
	return usageError("config: unknown subcommand " + args[0])
}
