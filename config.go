// Configuration: $XDG_CONFIG_HOME/pr-owl/config.yaml (or $PR_OWL_CONFIG).
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
	Tmux         TmuxConfig   `yaml:"tmux"`
	WorktreesDir string       `yaml:"worktrees_dir"`
	DefaultRepo  string       `yaml:"default_repo"`
	Agent        AgentConfig  `yaml:"agent"`
	OpenCmd      string       `yaml:"open_cmd"`
	Hooks        HooksConfig  `yaml:"hooks"`
	Theme        ThemeConfig  `yaml:"theme"`
	Keys         KeysConfig   `yaml:"keys"`
	Links        []LinkConfig `yaml:"links"`
}

type TmuxConfig struct {
	Session         string `yaml:"session"`
	KeepaliveWindow string `yaml:"keepalive_window"`
	StateOption     string `yaml:"state_option"`
}

type AgentConfig struct {
	Cmd       string   `yaml:"cmd"`
	Prompt    string   `yaml:"prompt"`
	LinkLocal []string `yaml:"link_local"`
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
	AfterOpen string `yaml:"after_open"`
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
// `quit: [q, ctrl+c]`. A single name round-trips as a scalar.
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

func (k keyNames) MarshalYAML() (any, error) {
	if len(k) == 1 {
		return k[0], nil
	}
	return []string(k), nil
}

// configTemplate is what `pr-owl config init` writes. It is the
// documentation for every key, and TestTemplateMatchesDefaults keeps it
// equal to defaultConfig().
const configTemplate = `# pr-owl configuration. Every key is optional; these are the defaults.

tmux:
  session: pr-reviews            # session that holds one window per review
  keepalive_window: scratch      # window that keeps the session alive with no reviews open
  state_option: "@claude-state"  # window option written by tmux-claude-status

worktrees_dir: .worktrees.local  # where review worktrees go, relative to the repo root
default_repo: ""                 # repo to use when pr-owl is started outside a git repo; ~ is expanded

agent:
  cmd: claude --permission-mode auto     # pr-owl appends -c (continue) when the worktree has a prior conversation
  prompt: "/pr-review:pr-review {pr}"    # first prompt of a fresh review; {pr} is the PR number
  link_local:                            # globs relative to the repo root, symlinked into each new worktree
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open

hooks:
  after_open: ""                 # command run after ` + "`pr-owl open`" + ` with PR_OWL_PR, PR_OWL_SESSION, PR_OWL_WINDOW, PR_OWL_WORKTREE, PR_OWL_REPO set; ~ is expanded

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
	c.Tmux = TmuxConfig{Session: "pr-reviews", KeepaliveWindow: "scratch", StateOption: "@claude-state"}
	c.WorktreesDir = ".worktrees.local"
	c.Agent = AgentConfig{
		Cmd:       "claude --permission-mode auto",
		Prompt:    "/pr-review:pr-review {pr}",
		LinkLocal: []string{".claude/settings.local.json", ".claude/*.local.md", ".claude/skills/*.local"},
	}
	c.Theme = ThemeConfig{Working: "#dbbc7f", Blocked: "214", Done: "42"}
	c.Keys = KeysConfig{
		Up: keyNames{"up", "k"}, Down: keyNames{"down", "j"},
		Top: keyNames{"g", "home"}, Bottom: keyNames{"G", "end"},
		PageUp: keyNames{"pgup", "ctrl+u"}, PageDown: keyNames{"pgdown", "ctrl+d"},
		Open: keyNames{"enter"}, Feedback: keyNames{"f"}, Browser: keyNames{"o"},
		Yank: keyNames{"y"}, Next: keyNames{"n"}, Cleanup: keyNames{"c"},
		Search: keyNames{"/"}, Cancel: keyNames{"esc"}, Refresh: keyNames{"r"}, Help: keyNames{"?"},
		Quit: keyNames{"q", "ctrl+c"},
	}
	return c
}

// configPath is $PR_OWL_CONFIG, else $XDG_CONFIG_HOME/pr-owl/config.yaml,
// else ~/.config/pr-owl/config.yaml.
func configPath() (string, error) {
	if p := os.Getenv("PR_OWL_CONFIG"); p != "" {
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
	return filepath.Join(base, "pr-owl", "config.yaml"), nil
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
	cfg.Hooks.AfterOpen = expandHome(cfg.Hooks.AfterOpen)
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
		{"tmux.state_option", cfg.Tmux.StateOption},
		{"worktrees_dir", cfg.WorktreesDir},
		{"agent.cmd", cfg.Agent.Cmd},
		{"agent.prompt", cfg.Agent.Prompt},
	}
	for _, r := range required {
		if r.value == "" {
			return fmt.Errorf("%s must not be empty", r.name)
		}
	}
	if d := filepath.Clean(cfg.WorktreesDir); filepath.IsAbs(d) || d == ".." || strings.HasPrefix(d, "../") {
		return fmt.Errorf("worktrees_dir must be a relative path inside the repo, got %q", cfg.WorktreesDir)
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

// runConfig implements `pr-owl config init | path | get <key>`,
// writing results to w.
func runConfig(args []string, w io.Writer) error {
	if len(args) == 0 {
		return usageError("config: expected init, path or get <key>")
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
	case "get":
		if len(args) != 2 {
			return usageError("config get: expected one key, e.g. tmux.session")
		}
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		out, err := configGet(cfg, args[1])
		if err != nil {
			return err
		}
		fmt.Fprint(w, out)
		return nil
	}
	return usageError("config: unknown subcommand " + args[0])
}

// configGet renders the value at a dotted key: a scalar verbatim plus
// newline (so shell `$(pr-owl config get default_repo)` gets the raw
// string), anything nested as YAML. Walks the YAML form of the config
// so the keys are the ones in the file, not the Go field names.
func configGet(cfg Config, key string) (string, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	node := doc.Content[0]
	for _, part := range strings.Split(key, ".") {
		node = mappingValue(node, part)
		if node == nil {
			return "", fmt.Errorf("no such key: %s", key)
		}
	}
	if node.Kind == yaml.ScalarNode {
		return node.Value + "\n", nil
	}
	out, err := yaml.Marshal(node)
	return string(out), err
}

// mappingValue returns the value node for key in a mapping node, or
// nil when n isn't a mapping or has no such key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	if n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
