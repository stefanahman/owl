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
	Mux          string         `yaml:"mux"`
	Tmux         TmuxConfig     `yaml:"tmux"`
	Herdr        HerdrConfig    `yaml:"herdr"`
	Remote       string         `yaml:"remote"`
	WorktreesDir string         `yaml:"worktrees_dir"`
	DefaultRepo  string         `yaml:"default_repo"`
	Agent        AgentConfig    `yaml:"agent"`
	OpenCmd      string         `yaml:"open_cmd"`
	OnOpen       string         `yaml:"on_open"`
	Hooks        HooksConfig    `yaml:"hooks"`
	Theme        ThemeConfig    `yaml:"theme"`
	Keys         KeysConfig     `yaml:"keys"`
	Bindings     BindingsConfig `yaml:"bindings"`
	Linear       LinearConfig   `yaml:"linear"`
	Issue        IssueConfig    `yaml:"issue"`
}

// IssueConfig is the feature side: where feature windows live under
// tmux, and the agent's first prompt on an issue.
type IssueConfig struct {
	Session string `yaml:"session"`
	Prompt  string `yaml:"prompt"`
}

// LinearConfig is the issue tracker: the key, as a reference, and the
// team new issues go to.
type LinearConfig struct {
	Token   string `yaml:"token"`
	Account string `yaml:"account"`
	Team    string `yaml:"team"`
}

type TmuxConfig struct {
	Session         string `yaml:"session"`
	KeepaliveWindow string `yaml:"keepalive_window"`
}

type HerdrConfig struct {
	Socket string `yaml:"socket"`
}

type AgentConfig struct {
	Cmd       string   `yaml:"cmd"`
	Prompt    string   `yaml:"prompt"`
	LinkLocal []string `yaml:"link_local"`
}

// BindingsConfig is the user's keys on a row, one list per list: what
// a key does to a PR and what it does to an issue are different
// things, and a key may mean one thing here and another there.
type BindingsConfig struct {
	PR    []Binding `yaml:"pr"`
	Issue []Binding `yaml:"issue"`
}

// Binding is one key on a row and what it does: a prompt handed to
// the agent — a fresh workspace starts with it, a running Claude
// receives it, a blocked one refuses it — or a URL opened. The help
// view lists bindings by name.
type Binding struct {
	Key     keyNames `yaml:"key"`
	Name    string   `yaml:"name"`
	Prompt  string   `yaml:"prompt"`
	URL     string   `yaml:"url"`
	Pattern string   `yaml:"pattern"` // {id} is its first match in the row's title, body and branch
	When    string   `yaml:"when"`    // conversation: only where a workspace or a prior conversation exists

	re *regexp.Regexp // compiled Pattern; nil when Pattern is empty
}

// whenConversation is the one condition a binding can carry: the key
// applies to work that has been started, never starts it.
const whenConversation = "conversation"

// text is the binding's prompt or URL, whichever it is.
func (b Binding) text() string {
	if b.URL != "" {
		return b.URL
	}
	return b.Prompt
}

// expand fills the binding's placeholders from a row's values, and
// {id} from the pattern's first match in haystack. ok is false when
// the pattern matches nothing — the key then does nothing.
func (b Binding) expand(values map[string]string, haystack string) (string, bool) {
	id := ""
	if b.re != nil {
		if id = b.re.FindString(haystack); id == "" {
			return "", false
		}
	}
	pairs := []string{"{id}", id}
	for k, v := range values {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(b.text()), true
}

// forPR expands the binding for a PR: {pr}, {repo}, {branch}, {url},
// {id} over title, body and branch.
func (b Binding) forPR(repo string, pr *PR) (string, bool) {
	return b.expand(map[string]string{"pr": strconv.Itoa(pr.Number), "repo": repo, "branch": pr.HeadRefName, "url": pr.URL},
		pr.Title+"\n"+pr.Body+"\n"+pr.HeadRefName)
}

// forIssue expands the binding for an issue: {key}, {repo}, {branch},
// {url}, {id} over title and branch.
func (b Binding) forIssue(repo string, is *Issue) (string, bool) {
	return b.expand(map[string]string{"key": is.Key, "repo": repo, "branch": is.Branch, "url": is.URL},
		is.Title+"\n"+is.Branch)
}

// mergeBindings lays the user's bindings over the defaults by key: a
// user entry with a default's first key replaces it in place, the
// others follow in the user's order.
func mergeBindings(defaults, user []Binding) []Binding {
	out := append([]Binding(nil), defaults...)
	for _, b := range user {
		replaced := false
		for i := range defaults { // only a shipped entry is replaced; two of the user's own collide in validate
			if d := out[i]; len(d.Key) > 0 && len(b.Key) > 0 && d.Key[0] == b.Key[0] {
				out[i] = b
				replaced = true
				break
			}
		}
		if !replaced {
			out = append(out, b)
		}
	}
	return out
}

// feedbackPrompt is what the shipped `f` binding sends. Two-pass
// self-critique + a RESOLVED taxonomy, calm tenor: encouraging
// language increases deliberation, urgency causes shortcuts.
const feedbackPrompt = "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict."

// defaultBindings are the keys owl ships on a row; a user's entry
// with the same key replaces one.
func defaultBindings() BindingsConfig {
	return BindingsConfig{PR: []Binding{{Key: keyNames{"f"}, Name: "check feedback", Prompt: feedbackPrompt, When: whenConversation}}}
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

// validateBindings rejects actions and bindings with no key, and a key
// bound twice within one list — the built-in actions and that list's
// bindings; a key may differ between the PR list and the issue list.
func validateBindings(k KeysConfig, b BindingsConfig) error {
	var errs []error
	scope := func(name string, list []Binding) {
		bound := map[string]string{} // key name → "keys.quit" / "bindings.pr[0]"
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
		for i, e := range list {
			claim(fmt.Sprintf("bindings.%s[%d]", name, i), e.Key)
		}
	}
	scope("pr", b.PR)
	scope("issue", b.Issue)
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
  prompt: "/owl:review {pr}"    # first prompt of a fresh review; {pr} is the PR number
  link_local:                            # globs relative to the repo root, symlinked into each new worktree
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

issue:
  session: features              # tmux: one window per feature lives here; herdr and cmux need no container
  prompt: "/owl:feature {key}"   # first prompt of a fresh feature; {key} is the issue's key (BAR-123)

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: auto                    # the TUI once an open starts: auto (quit under tmux, where a popup closes at once and open finishes behind it; stay under herdr and cmux, where the list keeps its own workspace), quit, stay (keep the list), switch (move your client to the reviews, for owl in a tmux window)

hooks:
  after_open: ""                 # command run after an open with OWL_PR (a review) or OWL_ISSUE and OWL_BRANCH (a feature), OWL_WINDOW, OWL_WORKTREE, OWL_REPO, OWL_MUX set, and OWL_SESSION under tmux and herdr; ~ is expanded.
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
  browser: o
  yank: y
  next: n
  cleanup: c
  search: /
  cancel: esc
  refresh: r
  help: "?"
  quit: [q, ctrl+c]

linear:                          # the issue tracker behind ` + "`owl issue`" + `
  token: ""                      # a personal API key (Linear: Settings → Security & access) as a reference: op://<vault>/<item>/<field> is read from 1Password once and kept in ~/.local/state/owl/linear.token, mode 600; file://<path> reads a file of yours (mode 600); $VAR reads the environment; anything else is the key itself
  account: ""                    # the 1Password account the item is in (its sign-in address), when more than one is signed in
  team: ""                       # the team's key (BAR in BAR-123): where ` + "`owl hoot`" + ` files issues

bindings:                        # your own keys on a row: a prompt handed to the agent, or a URL opened; ` + "`?`" + ` lists them by name
  pr:                            # on a PR; placeholders {pr}, {repo}, {branch}, {url}, and {id} — the first match of ` + "`pattern`" + ` in the title, body and branch (no match → the key does nothing)
    - key: f                     # shipped; an entry of yours with the same key replaces it, other keys add to it
      name: check feedback
      prompt: "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict."
      when: conversation         # only where a workspace or a prior conversation exists; without it, a fresh workspace starts with the prompt
    # - key: d
    #   name: Dependabot
    #   prompt: "/owl:dependabot {pr}"
    # - key: l
    #   name: Linear
    #   pattern: 'PROJ-\d+'
    #   url: https://linear.app/<org>/issue/{id}
  # issue:                       # on an issue; placeholders {key}, {repo}, {branch}, {url}, {id} (pattern over title and branch)
  #   - key: p
  #     name: Continue
  #     prompt: "Continue {key} where the last session left off"
  #     when: conversation
`

func defaultConfig() Config {
	var c Config
	c.Mux = "auto"
	c.Tmux = TmuxConfig{Session: "reviews", KeepaliveWindow: "scratch"}
	c.Remote = "origin"
	c.Issue = IssueConfig{Session: "features", Prompt: "/owl:feature {key}"}
	c.WorktreesDir = ".worktrees.local"
	c.Agent = AgentConfig{
		Cmd:       "claude --permission-mode auto",
		Prompt:    "/owl:review {pr}",
		LinkLocal: []string{".claude/settings.local.json", ".claude/*.local.md", ".claude/skills/*.local"},
	}
	c.Bindings = defaultBindings()
	c.OnOpen = "auto"
	c.Theme = ThemeConfig{Working: "#dbbc7f", Blocked: "214", Done: "42"}
	c.Keys = KeysConfig{
		Up: keyNames{"up", "k"}, Down: keyNames{"down", "j"},
		Top: keyNames{"g", "home"}, Bottom: keyNames{"G", "end"},
		PageUp: keyNames{"pgup", "ctrl+u"}, PageDown: keyNames{"pgdown", "ctrl+d"},
		Open: keyNames{"enter"}, Start: keyNames{"s"}, Browser: keyNames{"o"},
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

// moved is what a config from before bindings may still say, and
// where each of it went; said before the strict decode, so the
// message is this and not "field not found".
var moved = []struct{ path, to string }{
	{"links", "links moved to bindings.pr, one entry per link with url:"},
	{"keys.feedback", "keys.feedback moved to bindings.pr: the key of the shipped feedback binding"},
	{"agent.feedback_prompt", "agent.feedback_prompt moved to bindings.pr: the prompt of the shipped feedback binding"},
}

// movedKeys reports the first key of the old shape found in the YAML.
func movedKeys(data []byte) error {
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil // the strict decode reports the real problem
	}
	for _, m := range moved {
		node := any(doc)
		found := true
		for _, part := range strings.Split(m.path, ".") {
			mp, ok := node.(map[string]any)
			if !ok {
				found = false
				break
			}
			if node, ok = mp[part]; !ok {
				found = false
				break
			}
		}
		if found {
			return errors.New(m.to)
		}
	}
	return nil
}

// parseConfig overlays YAML onto the defaults and validates the result.
func parseConfig(data []byte) (Config, error) {
	if err := movedKeys(data); err != nil {
		return Config{}, err
	}
	cfg := defaultConfig()
	// The bindings the user writes merge with the shipped ones by key;
	// decoded on their own so the defaults are not simply replaced.
	defaults := cfg.Bindings
	cfg.Bindings = BindingsConfig{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// io.EOF: no document at all (empty file, comments only).
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, err
	}
	cfg.Bindings.PR = mergeBindings(defaults.PR, cfg.Bindings.PR)
	cfg.Bindings.Issue = mergeBindings(defaults.Issue, cfg.Bindings.Issue)
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
	case "auto", "quit", "stay", "switch":
	default:
		return fmt.Errorf("on_open must be auto, quit, stay or switch, got %q", cfg.OnOpen)
	}
	switch cfg.Mux {
	case "auto", "tmux", "herdr", "cmux":
	default:
		return fmt.Errorf("mux must be auto, tmux, herdr or cmux, got %q", cfg.Mux)
	}
	if cfg.Issue.Session == "" {
		return errors.New("issue.session must name the tmux session for features")
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
	for _, scope := range []struct {
		name string
		list []Binding
	}{{"pr", cfg.Bindings.PR}, {"issue", cfg.Bindings.Issue}} {
		for i := range scope.list {
			b := &scope.list[i]
			where := fmt.Sprintf("bindings.%s[%d]", scope.name, i)
			if b.Name == "" {
				return fmt.Errorf("%s: name is required", where)
			}
			if (b.Prompt == "") == (b.URL == "") {
				return fmt.Errorf("%s: exactly one of prompt and url", where)
			}
			if b.When != "" && b.When != whenConversation {
				return fmt.Errorf("%s: when must be conversation or absent, got %q", where, b.When)
			}
			if b.Pattern != "" {
				re, err := regexp.Compile(b.Pattern)
				if err != nil {
					return fmt.Errorf("%s.pattern: %w", where, err)
				}
				b.re = re
			} else if strings.Contains(b.text(), "{id}") {
				return fmt.Errorf("%s: {id} needs a pattern", where)
			}
		}
	}
	return validateBindings(cfg.Keys, cfg.Bindings)
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
