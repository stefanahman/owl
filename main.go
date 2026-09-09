// owl — TUI overview of the PRs where you're a reviewer, or of the
// issues assigned to you, with local state (worktree, window in the
// multiplexer, Claude activity) overlaid: review involvement (approved
// / engaged / any-CR) on a PR, the branch's PR on an issue.
//
// Widgets used, all from charm.land/bubbles/v2:
//
//	bubbles/viewport  scroll container (slice-based rendering, table pattern)
//	bubbles/textinput search field
//	bubbles/spinner   loading indicator
//	bubbles/key       key bindings (rebindable, drive the help view)
//	bubbles/help      auto-generates key legend from the keymap
package main

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// ------------------------------------------------------------
// Messages
// ------------------------------------------------------------

// prsMsg and mergedMsg carry the round (model.fetchGen) they were
// started in; a result from an older round is ignored, so a slow
// fetch can't overwrite a newer one.
type prsMsg struct {
	gen int
	prs []PR
}
type mergedMsg struct {
	gen int
	prs []PR
}
type userMsg string // authenticated user login

// issuesMsg and issuePRsMsg are the issue list's fetches: the open
// issues assigned to the user, and the repo's open PRs, which the rows
// show against the issue key their head branch carries.
type issuesMsg struct {
	gen    int
	issues []Issue
}
type issuePRsMsg struct {
	gen int
	prs []PR
}

// doneMsg carries the issues completed inside doneWindow — the issue
// list's answer to mergedMsg.
type doneMsg struct {
	gen    int
	issues []Issue
}

// projectsMsg is the project list's fetch: the open projects the user
// works in.
type projectsMsg struct {
	gen      int
	projects []Project
}

// localTickMsg asks for the local overlay again. Claude's state is
// whatever the multiplexer reports as Claude works; following it every
// two seconds — a status bar's cadence — lets a © change colour as
// Claude starts, gets blocked or finishes, without a key press.
type localTickMsg struct{}

const localRefreshEvery = 2 * time.Second

func localTick() tea.Cmd {
	return tea.Tick(localRefreshEvery, func(time.Time) tea.Msg { return localTickMsg{} })
}

// errMsg is a failed fetch: the list is stale (or, with nothing to
// show yet, absent) until a fetch succeeds.
type errMsg struct {
	gen int
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

// noticeMsg is the outcome of a user action — opening a URL, copying,
// switching client, open, close — shown in the action row until the
// next key press. It never replaces the list.
type noticeMsg struct{ err error }

// openedMsg / closedMsg report the end of an `open` / `close` child
// for a workspace — a PR's number or an issue's key — nil, or its
// failure with the child's stderr.
type openedMsg struct {
	id  string
	err error
}
type closedMsg struct {
	id  string
	err error
}

// ------------------------------------------------------------
// Keymap
// ------------------------------------------------------------

type keyMap struct {
	Up       key.Binding
	Down     key.Binding
	Home     key.Binding
	End      key.Binding
	PageUp   key.Binding
	PageDown key.Binding
	Enter    key.Binding
	Start    key.Binding
	Browser  key.Binding
	Yank     key.Binding
	Next     key.Binding
	Cleanup  key.Binding
	Search   key.Binding
	Cancel   key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Quit     key.Binding
	Bindings []key.Binding // parallel to the list's Config.Bindings
}

// newKeyMap builds the key bindings from `keys` and one list's
// `bindings`.
func newKeyMap(k KeysConfig, bindings []Binding) keyMap {
	bind := func(keys keyNames, desc string) key.Binding {
		return key.NewBinding(key.WithKeys(keys...), key.WithHelp(keys.label(), desc))
	}
	km := keyMap{
		Up:       bind(k.Up, "up"),
		Down:     bind(k.Down, "down"),
		Home:     bind(k.Top, "top"),
		End:      bind(k.Bottom, "bottom"),
		PageUp:   bind(k.PageUp, "page up"),
		PageDown: bind(k.PageDown, "page down"),
		Enter:    bind(k.Open, "open review"),
		Start:    bind(k.Start, "start (stay)"),
		Browser:  bind(k.Browser, "open PR in browser"),
		Yank:     bind(k.Yank, "yank PR URL"),
		Next:     bind(k.Next, "next attention-needed"),
		Cleanup:  bind(k.Cleanup, "clean up worktree"),
		Search:   bind(k.Search, "search"),
		Cancel:   bind(k.Cancel, "cancel/clear"),
		Refresh:  bind(k.Refresh, "refresh"),
		Help:     bind(k.Help, "help"),
		Quit:     bind(k.Quit, "quit"),
	}
	for _, b := range bindings {
		km.Bindings = append(km.Bindings, bind(b.Key, b.Name))
	}
	return km
}

var keyGlyphs = map[string]string{"up": "↑", "down": "↓", "left": "←", "right": "→", "enter": "↵", "pgdown": "pgdn"}

// label renders key names for the help views: the first key always,
// further keys only when they are single characters (`↑/k`, but `q`
// rather than `q/ctrl+c` — named alternates are noise in a legend).
func (k keyNames) label() string {
	var parts []string
	for i, name := range k {
		if i > 0 && len([]rune(name)) != 1 {
			continue
		}
		if g, ok := keyGlyphs[name]; ok {
			name = g
		}
		parts = append(parts, name)
	}
	return strings.Join(parts, "/")
}

// ShortHelp drives the footer legend: the built-in actions. FullHelp
// is rendered inside the `?` modal (helpModalView) via
// help.FullHelpView, and lists the user's bindings by name.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Next, k.Enter, k.Start, k.Browser, k.Search, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	actions := append([]key.Binding{k.Enter, k.Start, k.Browser, k.Yank, k.Cleanup}, k.Bindings...)
	return [][]key.Binding{
		{k.Up, k.Down, k.Next, k.Home, k.End, k.PageUp, k.PageDown},
		actions,
		{k.Search, k.Cancel, k.Refresh, k.Help, k.Quit},
	}
}

// ------------------------------------------------------------
// Styles
// ------------------------------------------------------------

// Claude-state styles follow the agent-state vocabulary (working /
// blocked / done / idle) the multiplexer reports. Their colours come
// from the `theme` config (applyTheme); everything else is fixed.
var (
	styleClaudeWorking lipgloss.Style // ©  Claude actively processing
	styleClaudeBlocked lipgloss.Style // ©  Claude waiting on you (permission, question, plan)
	styleClaudeDone    lipgloss.Style // ©  Claude finished (done + `*`, or idle without)

	styleWorktree      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // blue    ⎇  worktree present
	styleClaudeNeutral = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // gray    ©  session exists, no state (fresh window)
	styleApproved      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green   ✓  I approved (current verdict)
	styleReviewStale   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // amber   ✓ or ·  my review predates the head
	styleChangesReqd   = lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // orange  ⚠  any reviewer requested changes
	styleDim           = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleHeader        = lipgloss.NewStyle().Bold(true)
	styleSectionTodo   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")) // amber
	styleSectionWait   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))  // blue
	styleSectionOK     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))  // green
	styleSectionMerged = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141")) // purple (GitHub's merged color)
	styleDraft         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))            // dim for [draft]
	styleSearchLabel   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func applyTheme(t ThemeConfig) {
	styleClaudeWorking = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Working))
	styleClaudeBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Blocked))
	styleClaudeDone = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Done))
}

// ------------------------------------------------------------
// Row model
// ------------------------------------------------------------

// visibleRow is a section header, a PR row, an issue row or a project
// row. On a header sectionTitle is what it says; on an issue or
// project row it is the section the row sits in, which decides whether
// the row repeats its state name.
type visibleRow struct {
	sectionTitle string
	sectionNote  string // dim, after the title: the Done section's window
	sectionStyle lipgloss.Style
	pr           *PR
	status       ReviewStatus
	merged       bool
	issue        *Issue
	project      *Project
}

// header reports whether the row is a section title.
func (r visibleRow) header() bool { return r.pr == nil && r.issue == nil && r.project == nil }

// id names what the row is about, for inflight and the children: a
// PR's number, an issue's key, a project's slug.
func (r visibleRow) id() string {
	if r.pr != nil {
		return strconv.Itoa(r.pr.Number)
	}
	if r.issue != nil {
		return r.issue.Key
	}
	if r.project != nil {
		return r.project.SlugID
	}
	return ""
}

// ------------------------------------------------------------
// Model
// ------------------------------------------------------------

type model struct {
	cfg Config
	// kind is the list: "pr" (the PRs waiting for your review) or
	// "issue" (the issues assigned to you); sc the scope of workspaces
	// it overlays.
	kind    string
	sc      scope
	tracker Tracker // the issue list's source; nil on the PR list

	// domain data
	repo       string // owner/name on GitHub
	repoDir    string // the repository's main working tree; "" outside a repo
	me         string
	prs        []PR
	merged     []PR
	issues     []Issue
	doneIssues []Issue         // completed inside doneWindow; the Done section
	projects   []Project       // the project list's rows
	issuePRs   map[string][]PR // the repo's open PRs by issue key, for the issue rows
	localState map[string]LocalState

	// load state
	ready       bool      // the list has been fetched (or read from the cache)
	fetchGen    int       // the current fetch round; results from older rounds are ignored
	refreshing  bool      // r pressed: the list stays, the action row spins
	err         error     // the last fetch failure; cleared by a successful fetch or r
	notice      error     // the last action failure; cleared by the next key press
	lastFetched time.Time // set when prsMsg lands; drives "updated X ago"

	// UI state
	cursor   int               // index into the current visibleRows() output
	inflight map[string]string // row id → "opening #42…": open/close children running; the list stays usable

	// widgets
	keys    keyMap
	help    help.Model
	search  textinput.Model
	list    viewport.Model
	spinner spinner.Model

	showHelp bool // true → full-screen help modal covers the list

	width, height int

	// noInit makes Init() do nothing: tests feed messages themselves.
	noInit bool

	// farewell is printed by main after the TUI exits: what `open` did,
	// for the terminal the popup leaves behind.
	farewell string

	// runSelf runs this binary with args (`open`, `close`). Tests
	// replace it — os.Executable() is the test binary there.
	runSelf func(kind string, args ...string) error
}

// initialModel gathers what the model needs from the environment —
// the repo of the working directory and its cache — and builds it.
func initialModel(cfg Config) model {
	repo := currentRepo(cfg.Remote)
	m := newModel(cfg, repo, loadCache(repo))
	m.repoDir, _ = mainRepo(".") // "" outside a repo: nothing to resume
	return m
}

// initialIssueModel is initialModel for the issue list: the tracker,
// and the issue cache.
func initialIssueModel(cfg Config, tracker Tracker) model {
	repo := currentRepo(cfg.Remote)
	m := newIssueModel(cfg, repo, tracker, loadIssueCache())
	m.repoDir, _ = mainRepo(".")
	return m
}

// initialProjectModel is newProjectModel with the repo, the working
// tree and the project cache.
func initialProjectModel(cfg Config, tracker Tracker) model {
	m := newProjectModel(cfg, currentRepo(cfg.Remote), tracker, loadProjectCache())
	m.repoDir, _ = mainRepo(".")
	return m
}

// newProjectModel builds the project list's model. Tests call it
// directly.
func newProjectModel(cfg Config, repo string, tracker Tracker, cache *projectCacheFile) model {
	m := newModel(cfg, repo, nil)
	m.kind, m.sc, m.tracker = "project", features, tracker
	m.search.Placeholder = "project name"
	m.search.CharLimit = 64
	m.keys = newKeyMap(cfg.Keys, nil)
	m.keys.Browser.SetHelp(m.keys.Browser.Help().Key, "open project in Linear")
	m.keys.Yank.SetHelp(m.keys.Yank.Help().Key, "yank project name")
	if cache != nil {
		m.projects = cache.Projects
		m.issues = cache.Issues
		m.ready = true
		m.lastFetched = cache.FetchedAt
		m.cursor = cache.Cursor
		m.clampCursor()
	}
	return m
}

// newIssueModel builds the issue list's model. Tests call it directly.
func newIssueModel(cfg Config, repo string, tracker Tracker, cache *issueCacheFile) model {
	m := newModel(cfg, repo, nil)
	m.kind, m.sc, m.tracker = "issue", features, tracker
	m.search.Placeholder = "key or title"
	m.search.CharLimit = 64
	m.keys = newKeyMap(cfg.Keys, cfg.Bindings.Issue)
	// The legend names what the keys do here: features, not reviews.
	m.keys.Enter.SetHelp(m.keys.Enter.Help().Key, "open feature")
	m.keys.Browser.SetHelp(m.keys.Browser.Help().Key, "open issue in browser")
	if cache != nil {
		m.issues = cache.Issues
		m.doneIssues = cache.DoneIssues
		m.issuePRs = byIssueKey(cache.IssuePRs)
		m.ready = true
		m.lastFetched = cache.FetchedAt
		m.cursor = cache.Cursor
		m.clampCursor()
	}
	return m
}

// newModel builds the PR list's model from its inputs. Tests call it
// directly, so nothing in here shells out or reads the cache.
func newModel(cfg Config, repo string, cache *cacheFile) model {
	applyTheme(cfg.Theme)

	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "PR number"
	ti.CharLimit = 16
	// Note: bubbles/textinput.Validate is display-only (sets m.Err,
	// doesn't reject). Digit-only filtering lives upstream in
	// handleKey — non-digit runes are dropped before reaching textinput.

	// The braille spinner: the default |/-\ bar is one thin cell that
	// barely reads as motion in a row.
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	sp.Style = styleDim

	m := model{
		cfg:      cfg,
		kind:     "pr",
		sc:       reviews,
		repo:     repo,
		keys:     newKeyMap(cfg.Keys, cfg.Bindings.PR),
		help:     help.New(),
		search:   ti,
		list:     viewport.New(),
		spinner:  sp,
		inflight: map[string]string{},
	}
	// kind comes from the caller: the model this closure sees is the
	// one under construction, a review list, and the issue list is
	// made from it afterwards.
	m.runSelf = func(kind string, args ...string) error {
		return runChild(newWindows(cfg, scopeOf(kind)).ChildEnv(), append(append(globalArgs(), kind), args...)...)
	}
	// Cache-first: if a previous session left a cache for this repo,
	// seed the state so the popup renders instantly. The live fetches
	// still fire in Init and overwrite the state when they land — the
	// "updated Xm ago" indicator shows freshness so the user sees when
	// they're looking at stale data.
	if cache != nil {
		m.prs = cache.Prs
		m.merged = cache.Merged
		m.me = cache.Me
		m.ready = true
		m.lastFetched = cache.FetchedAt
		// The row, not the PR: the list is where it was, so start where
		// the last session ended — clamped, in case it shrank.
		m.cursor = cache.Cursor
		m.clampCursor()
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.noInit {
		return nil
	}
	return tea.Batch(append(m.fetches(),
		m.fetchLocal,
		localTick(),
		m.spinner.Tick,
		textinput.Blink,
		tea.RequestBackgroundColor,
	)...)
}

// fetches are the list's remote fetches, for Init, refresh and focus.
func (m model) fetches() []tea.Cmd {
	switch m.kind {
	case "issue":
		return []tea.Cmd{m.fetchIssues, m.fetchIssuePRs, m.fetchDone}
	case "project":
		// The issues too: a row says how much of the project is yours.
		return []tea.Cmd{m.fetchProjects, m.fetchIssues}
	}
	return []tea.Cmd{m.fetchPRs, m.fetchMerged, fetchUser}
}

func fetchUser() tea.Msg { return userMsg(currentUser()) }

// persistCache writes the list's snapshot and the cursor row to its
// cache file. Called after a fetch lands, and once more on exit for
// the cursor, so the next popup startup has warm data and the same
// row selected. All errors swallowed inside the writer — cache is
// best-effort.
func (m model) persistCache() {
	if m.kind == "project" {
		saveProjectCache(projectCacheFile{Projects: m.projects, Issues: m.issues, FetchedAt: m.lastFetched, Cursor: m.cursor})
		return
	}
	if m.kind == "issue" {
		saveIssueCache(issueCacheFile{Issues: m.issues, DoneIssues: m.doneIssues, IssuePRs: flattenPRs(m.issuePRs), FetchedAt: m.lastFetched, Cursor: m.cursor})
		return
	}
	saveCache(m.repo, cacheFile{
		Prs:       m.prs,
		Merged:    m.merged,
		Me:        m.me,
		FetchedAt: m.lastFetched,
		Cursor:    m.cursor,
	})
}

// openWorkspace runs `owl <kind> open <id> [--prompt TEXT]` and reports
// when it ends. The child gets its own session (Setsid): owl usually
// runs inside a tmux popup, and with on_open: quit the popup closes
// the moment the child starts — it must finish on its own, and it
// does (see runChild for where its failure goes then).
func (m model) openWorkspace(id, prompt string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"open", id}
		if prompt != "" {
			args = append(args, "--prompt", prompt)
		}
		return openedMsg{id, m.runSelf(m.kind, args...)}
	}
}

// startWorkspace runs `owl <kind> start <id> [--prompt TEXT]`: the
// workspace comes up, or gets the prompt, and the list stays — for
// starting several one after another, and for f.
func (m model) startWorkspace(id, prompt string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"start", id}
		if prompt != "" {
			args = append(args, "--prompt", prompt)
		}
		return openedMsg{id, m.runSelf(m.kind, args...)}
	}
}

// closeWorkspace runs `owl <kind> close <id>`; the TUI refreshes its
// overlay when it succeeds. The agent's conversation survives on
// disk, so Enter / f afterwards resume it.
func (m model) closeWorkspace(id string) tea.Cmd {
	return func() tea.Msg {
		return closedMsg{id, m.runSelf(m.kind, "close", id)}
	}
}

// onOpen is what the TUI does once an open starts: the setting, or
// with auto what fits the multiplexer — quit under tmux, where the
// list is a popup that closes at once; stay under herdr and cmux,
// where the list has a workspace of its own and quitting would leave
// a dead shell there.
func (m model) onOpen() string {
	if m.cfg.OnOpen != "auto" {
		return m.cfg.OnOpen
	}
	if newWindows(m.cfg, m.sc).Kind() == "tmux" {
		return "quit"
	}
	return "stay"
}

// launch starts an open, start or close child for a row in the
// background. The list stays usable meanwhile; a second key on the
// same row is refused until the child reports. An open (arrive) with
// on_open: quit ends the TUI at once — the popup closes, the child
// finishes behind it.
func (m model) launch(id, label string, cmd tea.Cmd, arrive bool) (tea.Model, tea.Cmd) {
	if running, ok := m.inflight[id]; ok {
		m.notice = fmt.Errorf("still %s", running)
		return m, nil
	}
	m.inflight[id] = label
	if arrive && m.onOpen() == "quit" {
		m.farewell = label
		return m, tea.Batch(cmd, tea.Quit)
	}
	return m, tea.Batch(cmd, m.spinner.Tick)
}

// runChild runs this binary with args in its own session and returns
// nil, or its failure with the child's stderr in the message. env is
// the multiplexer's ChildEnv, so the child can notify the right way.
//
// The child outlives the TUI with on_open: quit, and a Go program
// writing to a broken pipe on stdout or stderr is killed by SIGPIPE —
// so its stdout is discarded rather than piped, and its failure also
// goes to the multiplexer before it is printed (see exitOn).
func runChild(env []string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate owl binary: %w", err)
	}
	// A test binary would run its whole suite as `owl pr open`, and that
	// suite would do it again. Tests inject runSelf; this catches the
	// one that forgets.
	if strings.HasSuffix(self, ".test") {
		return fmt.Errorf("%s is a test binary, not owl", self)
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("owl %s: %s", childCommand(args), msg)
	}
	return nil
}

// childCommand names the child's command line for a message with the
// prompt text elided: a prompt runs to a paragraph, and the reason for
// the failure comes after it, off the edge of the notice line.
func childCommand(args []string) string {
	shown := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--prompt" && i+1 < len(args):
			shown = append(shown, "--prompt …")
			i++
		case strings.HasPrefix(args[i], "--prompt="):
			shown = append(shown, "--prompt=…")
		default:
			shown = append(shown, args[i])
		}
	}
	return strings.Join(shown, " ")
}

// openPRInBrowser opens the PR's page. pr.URL comes from the API, so
// this is right on GitHub Enterprise too; it is empty only while a
// pre-url cache file is showing and the first fetch hasn't landed.
func (m model) openPRInBrowser(pr *PR) tea.Cmd {
	return func() tea.Msg {
		if pr.URL == "" {
			return noticeMsg{fmt.Errorf("PR #%d: URL not loaded yet", pr.Number)}
		}
		return openURL(m.cfg.OpenCmd, pr.URL)
	}
}

// bindings is the list's own: the PR list's or the issue list's.
func (m model) bindings() []Binding {
	switch m.kind {
	case "issue":
		return m.cfg.Bindings.Issue
	case "project":
		return nil // no per-row prompts until a project has a workspace
	}
	return m.cfg.Bindings.PR
}

// press does what a binding says on the selected row. A prompt goes
// through `start --prompt`, like s with words: a fresh workspace
// starts with it, a running Claude receives it, a blocked one refuses
// it — and the list stays. With `when: conversation` the key applies
// only to work that has been started: a workspace, or a conversation
// Claude kept on disk after the window went. A URL opens. A pattern
// that matches nothing makes the key a no-op.
func (m model) press(b Binding) (tea.Model, tea.Cmd) {
	var id, label, text string
	var ok bool
	switch {
	case m.selectedPR() != nil:
		pr := m.selectedPR()
		if b.When == whenConversation {
			ls := findLocalForPR(m.localState, pr.Number)
			if ls.Window == "" && !hasPriorConversation(m.repoDir, m.cfg.WorktreesDir, pr.Number) {
				return m, nil
			}
		}
		id, label = strconv.Itoa(pr.Number), fmt.Sprintf("#%d", pr.Number)
		text, ok = b.forPR(m.repo, pr)
	case m.selectedIssue() != nil:
		is := m.selectedIssue()
		if b.When == whenConversation {
			ls := findLocalBy(m.localState, func(name string) bool { return matchesIssue(name, is.Key) })
			if ls.Window == "" && ls.Worktree == "" && (m.repoDir == "" || !hasConversationFor(filepath.Join(m.repoDir, m.cfg.WorktreesDir, is.Branch))) {
				return m, nil
			}
		}
		id, label = is.Key, is.Key
		text, ok = b.forIssue(m.repo, is)
	default:
		return m, nil
	}
	if !ok {
		return m, nil
	}
	if b.URL != "" {
		return m, func() tea.Msg { return openURL(m.cfg.OpenCmd, text) }
	}
	return m.launch(id, fmt.Sprintf("%s on %s…", b.Name, label), m.startWorkspace(id, text), false)
}

// switchClient moves the user's client to the review container, whose
// current window `open` has just selected.
func switchClient(mx windows) tea.Cmd {
	return func() tea.Msg {
		if err := mx.SwitchClient(); err != nil {
			return noticeMsg{err}
		}
		return nil
	}
}

// openURL hands a URL to `open_cmd` (split on whitespace, URL
// appended), defaulting to the platform opener.
func openURL(openCmd, url string) tea.Msg {
	argv := strings.Fields(openCmd)
	if len(argv) == 0 {
		if runtime.GOOS == "darwin" {
			argv = []string{"open"}
		} else {
			argv = []string{"xdg-open"}
		}
	}
	argv = append(argv, url)
	if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
		return noticeMsg{fmt.Errorf("%s %s: %w", argv[0], url, err)}
	}
	return nil
}

// yankPRURL copies the PR's URL to the clipboard through OSC 52 — the
// terminal does the copying, so it works over SSH and inside tmux
// (`set-clipboard on`) without pbcopy or xclip.
func yankPRURL(pr *PR) tea.Cmd {
	if pr.URL == "" {
		return func() tea.Msg { return noticeMsg{fmt.Errorf("PR #%d: URL not loaded yet", pr.Number)} }
	}
	return tea.SetClipboard(pr.URL)
}

// ------------------------------------------------------------
// Update
// ------------------------------------------------------------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeViewport()
		m.refreshList()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.BackgroundColorMsg:
		// Bubbles no longer guess the terminal background; pick the
		// light or dark palette once the terminal has answered.
		m.help.Styles = help.DefaultStyles(msg.IsDark())
		m.search.SetStyles(textinput.DefaultStyles(msg.IsDark()))

	case tea.FocusMsg:
		// Window regained focus. If a search filter was active but the
		// input isn't focused (user typed Enter then switched away),
		// re-focus so they can keep editing without hunting for `/`.
		if m.search.Value() != "" && !m.search.Focused() {
			cmds = append(cmds, m.search.Focus())
		}
		// Auto-refresh on focus: kick the fetches so returning to the
		// window shows current state. Debounced at 2s so rapid
		// focus/unfocus (window-manager churn) doesn't storm gh.
		if time.Since(m.lastFetched) > 2*time.Second {
			m.fetchGen++
			cmds = append(cmds, append(m.fetches(), m.fetchLocal)...)
		}

	case spinner.TickMsg:
		// bubbles/spinner has no Stop method — you stop it by not
		// forwarding its next tick. It spins during the initial fetch
		// and while an open/close child runs.
		if !m.ready || len(m.inflight) > 0 || m.refreshing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		if len(m.inflight) > 0 {
			m.refreshList() // the rows in flight carry the spinner too
		}

	case prsMsg:
		if msg.gen != m.fetchGen {
			break // an older round; a newer one has landed or is coming
		}
		m.prs = msg.prs
		m.ready = true
		m.refreshing = false
		m.err = nil
		m.lastFetched = time.Now()
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case issuesMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.issues = msg.issues
		m.ready = true
		m.refreshing = false
		m.err = nil
		m.lastFetched = time.Now()
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case issuePRsMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.issuePRs = byIssueKey(msg.prs)
		m.refreshList()
		m.persistCache()

	case doneMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.doneIssues = msg.issues
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case projectsMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.projects = msg.projects
		m.ready = true
		m.refreshing = false
		m.err = nil
		m.lastFetched = time.Now()
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case mergedMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.merged = msg.prs
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case localMsg:
		m.localState = map[string]LocalState(msg)
		m.refreshList()
		// LocalState is derived from tmux/git — cheap to refetch, not cached.

	case localTickMsg:
		cmds = append(cmds, m.fetchLocal, localTick())

	case userMsg:
		m.me = string(msg)
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case errMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.err = msg.err
		m.ready = true
		m.refreshing = false

	case noticeMsg:
		m.notice = msg.err

	case openedMsg:
		delete(m.inflight, msg.id)
		if msg.err != nil {
			m.notice = msg.err
			break
		}
		// The workspace is up (with on_open: quit the TUI is already
		// gone). switch moves the user's client to the windows.
		if m.onOpen() == "switch" {
			cmds = append(cmds, switchClient(newWindows(m.cfg, m.sc)))
		}
		cmds = append(cmds, m.fetchLocal)

	case closedMsg:
		delete(m.inflight, msg.id)
		if msg.err != nil {
			m.notice = msg.err
			break
		}
		cmds = append(cmds, m.fetchLocal)
	}

	return m, tea.Batch(cmds...)
}

// handleKey routes a KeyMsg. When the search input is focused, the
// input consumes most keys — we only intercept esc/enter/tab-out.
func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Help modal captures everything: quit still quits; anything else
	// dismisses. Keeps the modal an obvious mode with a single exit.
	if m.showHelp {
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}

	if m.search.Focused() {
		// A quit key that isn't text (ctrl+c) still quits; a typed q is
		// input, which the digit filter below drops.
		if msg.Text == "" && key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		// esc/enter are the input's own keys, independent of what the
		// list actions are bound to.
		switch msg.Code {
		case tea.KeyEscape:
			m.search.SetValue("")
			m.search.Blur()
			m.clampCursor()
			m.refreshList()
			return m, nil
		case tea.KeyEnter:
			// Blur search (applies filter, keeps value).
			m.search.Blur()
			return m, nil
		}
		// The PR list filters by number: drop non-digit runes upstream —
		// bubbles/textinput's Validate only sets a display error, it
		// doesn't reject input. Non-rune keys (backspace, arrows, delete)
		// pass through so editing works. The issue list takes any text.
		if msg.Text != "" && m.kind == "pr" {
			for _, r := range msg.Text {
				if r < '0' || r > '9' {
					return m, nil
				}
			}
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		// Filter changed → cursor may need clamping and list re-render.
		m.cursor = m.firstPRRowIndex()
		m.refreshList()
		return m, cmd
	}

	m.notice = nil // a key press acknowledges the last action's outcome
	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Refresh):
		// The list stays while the new round runs — the same as the
		// refresh on focus, with a spinner because it was asked for.
		m.fetchGen++
		m.refreshing = true
		m.err = nil
		return m, tea.Batch(append(m.fetches(), m.fetchLocal, m.spinner.Tick)...)
	case key.Matches(msg, m.keys.Up):
		m.moveCursor(-1)
		m.refreshList()
	case key.Matches(msg, m.keys.Down):
		m.moveCursor(1)
		m.refreshList()
	case key.Matches(msg, m.keys.PageUp):
		m.moveCursor(-m.list.Height() / 2)
		m.refreshList()
	case key.Matches(msg, m.keys.PageDown):
		m.moveCursor(m.list.Height() / 2)
		m.refreshList()
	case key.Matches(msg, m.keys.Home):
		m.cursor = m.firstPRRowIndex()
		m.refreshList()
	case key.Matches(msg, m.keys.End):
		m.cursor = m.lastPRRowIndex()
		m.refreshList()
	case key.Matches(msg, m.keys.Enter), key.Matches(msg, m.keys.Start):
		// A project has no workspace yet: the conversation at that level
		// is the next piece, and Enter is the key it will land on.
		if m.kind == "project" {
			m.notice = fmt.Errorf("the project conversation is not built yet — o opens it in Linear")
			break
		}
		if row, ok := m.selectedRow(); ok {
			if key.Matches(msg, m.keys.Enter) {
				return m.launch(row.id(), "opening "+row.label()+"…", m.openWorkspace(row.id(), ""), true)
			}
			return m.launch(row.id(), "starting "+row.label()+"…", m.startWorkspace(row.id(), ""), false)
		}
	case key.Matches(msg, m.keys.Browser):
		if pr := m.selectedPR(); pr != nil {
			return m, m.openPRInBrowser(pr)
		}
		if is := m.selectedIssue(); is != nil {
			return m, func() tea.Msg { return openURL(m.cfg.OpenCmd, is.URL) }
		}
		if p := m.selectedProject(); p != nil {
			return m, func() tea.Msg { return openURL(m.cfg.OpenCmd, p.URL) }
		}
	case key.Matches(msg, m.keys.Yank):
		if pr := m.selectedPR(); pr != nil {
			return m, yankPRURL(pr)
		}
		if is := m.selectedIssue(); is != nil {
			return m, tea.SetClipboard(is.Key)
		}
		// The name, not the slug: it is what `owl issue --project` and a
		// search take.
		if p := m.selectedProject(); p != nil {
			return m, tea.SetClipboard(p.Name)
		}
	case key.Matches(msg, m.keys.Next):
		m.jumpToNextAttention()
		m.refreshList()
	case key.Matches(msg, m.keys.Cleanup):
		if row, ok := m.selectedRow(); ok {
			// Only fires cleanup if there's a local worktree/session to
			// tear down — otherwise it's a no-op and the errMsg would
			// just noise the UI.
			if ls := m.localOf(row); ls.Worktree != "" || ls.Window != "" {
				return m.launch(row.id(), "closing "+row.label()+"…", m.closeWorkspace(row.id()), false)
			}
		}
	case key.Matches(msg, m.keys.Search):
		return m, m.search.Focus()
	case key.Matches(msg, m.keys.Cancel):
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.clampCursor()
			m.refreshList()
		}
	case key.Matches(msg, m.keys.Help):
		m.showHelp = true
	default:
		for i, b := range m.keys.Bindings {
			if key.Matches(msg, b) {
				return m.press(m.bindings()[i])
			}
		}
	}
	return m, nil
}

// ------------------------------------------------------------
// Row navigation & scroll
// ------------------------------------------------------------

// moveCursor moves by delta, skipping section-header rows.
func (m *model) moveCursor(delta int) {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return
	}
	step := 1
	if delta < 0 {
		step = -1
		delta = -delta
	}
	for i := 0; i < delta; i++ {
		next := m.cursor + step
		for next >= 0 && next < len(rows) && rows[next].header() {
			next += step
		}
		if next < 0 || next >= len(rows) {
			break
		}
		m.cursor = next
	}
}

func (m *model) clampCursor() {
	if last := m.lastPRRowIndex(); m.cursor > last {
		if last < 0 {
			m.cursor = 0
		} else {
			m.cursor = last
		}
	}
	// Land on a row, not a header.
	rows := m.visibleRows()
	for m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].header() {
		m.cursor++
	}
	if m.cursor >= len(rows) {
		m.cursor = m.lastPRRowIndex()
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// selectedRow returns the row under the cursor, when it is a PR or an
// issue (not a header, not an empty list).
func (m model) selectedRow() (visibleRow, bool) {
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) || rows[m.cursor].header() {
		return visibleRow{}, false
	}
	return rows[m.cursor], true
}

// selectedPR returns the PR under the cursor, or nil.
func (m model) selectedPR() *PR {
	row, _ := m.selectedRow()
	return row.pr
}

// selectedIssue returns the issue under the cursor, or nil.
func (m model) selectedIssue() *Issue {
	row, _ := m.selectedRow()
	return row.issue
}

// selectedProject returns the project under the cursor, or nil.
func (m model) selectedProject() *Project {
	row, _ := m.selectedRow()
	return row.project
}

// label is how the action row names the row: #42, BAR-4159.
func (r visibleRow) label() string {
	if r.pr != nil {
		return "#" + strconv.Itoa(r.pr.Number)
	}
	return r.id()
}

// localOf is the row's overlay: the worktree, window and agent state
// of its workspace.
func (m model) localOf(r visibleRow) LocalState {
	if r.pr != nil {
		return findLocalForPR(m.localState, r.pr.Number)
	}
	if r.issue != nil {
		return findLocalBy(m.localState, func(name string) bool { return matchesIssue(name, r.issue.Key) })
	}
	return LocalState{}
}

// jumpToNextAttention advances the cursor to the next row that wants
// attention — Todo bucket, or any row whose Claude state is blocked
// (waiting on you) or done (unread). Wraps around when it hits the
// end. No-op when no attention-needed rows exist.
func (m *model) jumpToNextAttention() {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return
	}
	wants := func(r visibleRow) bool {
		if r.header() {
			return false
		}
		if r.pr != nil && r.status == StatusTodo && !r.merged {
			return true
		}
		ls := m.localOf(r)
		return ls.ClaudeState == agentBlocked || ls.ClaudeState == agentDone
	}
	// Scan forward from cursor+1, then wrap.
	for offset := 1; offset <= len(rows); offset++ {
		i := (m.cursor + offset) % len(rows)
		if wants(rows[i]) {
			m.cursor = i
			return
		}
	}
	// Nothing wants attention — leave cursor put.
}

func (m model) firstPRRowIndex() int {
	rows := m.visibleRows()
	for i := range rows {
		if !rows[i].header() {
			return i
		}
	}
	return 0
}

func (m model) lastPRRowIndex() int {
	rows := m.visibleRows()
	for i := len(rows) - 1; i >= 0; i-- {
		if !rows[i].header() {
			return i
		}
	}
	return -1
}

// ------------------------------------------------------------
// Layout
// ------------------------------------------------------------

// Chrome line counts. Both regions are fixed-height so scroll math
// stays simple.
func (m model) topChromeLines() int {
	// title(1) + action-row(1) + separator(1)
	return 3
}

func (m model) bottomChromeLines() int {
	// blank(1) + short-help line (1)
	return 2
}

// resizeViewport sets the viewport's width/height to fit the current
// terminal minus chrome. Called on WindowSizeMsg.
func (m *model) resizeViewport() {
	if m.width == 0 || m.height == 0 {
		return
	}
	h := m.height - m.topChromeLines() - m.bottomChromeLines()
	if h < 3 {
		h = 3
	}
	m.list.SetWidth(m.width)
	m.list.SetHeight(h)
	m.help.SetWidth(m.width)
	// Leave room for the "search /  " label; clamp so a very narrow
	// terminal doesn't hand textinput a negative width.
	m.search.SetWidth(clampInt(m.width-20, 10, 200))
}

// ------------------------------------------------------------
// Content rendering
// ------------------------------------------------------------

// visibleRows builds the section-grouped row list with the search
// filter applied. Sections with zero visible rows are omitted.
func (m model) visibleRows() []visibleRow {
	switch m.kind {
	case "issue":
		return m.visibleIssueRows()
	case "project":
		return m.visibleProjectRows()
	}
	filter := m.search.Value()

	matches := func(pr PR) bool {
		if filter == "" {
			return true
		}
		return strings.Contains(strconv.Itoa(pr.Number), filter)
	}

	groups := []struct {
		title  string
		style  lipgloss.Style
		status ReviewStatus
	}{
		{"Todo", styleSectionTodo, StatusTodo},
		{"Waiting for you", styleSectionTodo, StatusWaitingForYou}, // amber — same "you should act" bucket as Todo
		{"Waiting for author", styleSectionWait, StatusWaitingForAuthor},
		{"Approved", styleSectionOK, StatusApproved},
	}

	var out []visibleRow
	for _, g := range groups {
		var members []PR
		for _, pr := range m.prs {
			if pr.MyReviewStatus(m.me) != g.status {
				continue
			}
			if !matches(pr) {
				continue
			}
			members = append(members, pr)
		}
		if len(members) == 0 {
			continue
		}
		out = append(out, visibleRow{sectionTitle: g.title, sectionStyle: g.style})
		for i := range members {
			out = append(out, visibleRow{pr: &members[i], status: g.status})
		}
	}

	// Merged section is always last.
	var mergedVisible []PR
	for _, pr := range m.merged {
		if matches(pr) {
			mergedVisible = append(mergedVisible, pr)
		}
	}
	if len(mergedVisible) > 0 {
		title := "Merged (last " + mergedWindowLabel + ")"
		out = append(out, visibleRow{sectionTitle: title, sectionStyle: styleSectionMerged, merged: true})
		for i := range mergedVisible {
			out = append(out, visibleRow{pr: &mergedVisible[i], merged: true})
		}
	}

	return out
}

// refreshList slices the visible rows to fit the viewport and pushes
// them into it via SetContent. Slice-based rendering matches the
// pattern in bubbles/table.UpdateViewport: content in the viewport is
// always at YOffset 0; scrolling = re-slicing on cursor move.
func (m *model) refreshList() {
	rows := m.visibleRows()
	h := m.list.Height()
	if h == 0 || len(rows) == 0 {
		m.list.SetContent("")
		return
	}

	start := 0
	if m.cursor >= h {
		start = m.cursor - h + 1
	}
	if start+h > len(rows) {
		start = len(rows) - h
	}
	if start < 0 {
		start = 0
	}
	end := start + h
	if end > len(rows) {
		end = len(rows)
	}

	lines := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		lines = append(lines, m.renderRow(rows[i], i == m.cursor))
	}
	m.list.SetContent(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

func (m model) renderRow(row visibleRow, selected bool) string {
	if row.header() {
		if row.sectionNote != "" {
			return row.sectionStyle.Render(row.sectionTitle) + styleDim.Render(" · "+row.sectionNote)
		}
		return row.sectionStyle.Render(row.sectionTitle)
	}
	if row.issue != nil {
		return m.renderIssueRow(row, selected)
	}
	if row.project != nil {
		return m.renderProjectRow(row, selected)
	}
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	local := findLocalForPR(m.localState, row.pr.Number)
	starting := "" // the spinner takes the worktree slot while a child works on this PR
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	draft := ""
	if row.pr.IsDraft {
		draft = styleDraft.Render(" [draft]")
	}
	// The age sits in a three-cell column before the title, where the
	// eye already is; after a long title it landed far to the right.
	// Merged rows show the merge age — the section says merged.
	age := relativeAge(row.pr.UpdatedAt)
	if row.merged {
		age = relativeAge(row.pr.MergedAt)
	}
	return fmt.Sprintf(
		"%s#%-5d %s %s  %s%s (%s)",
		cursor,
		row.pr.Number,
		badges(local, starting, row.pr.IApproved(m.me), row.pr.IReviewed(m.me), row.status == StatusWaitingForYou, row.pr.HasChangesRequested()),
		styleDim.Render(fmt.Sprintf("%3s", age)),
		trim(row.pr.Title, 70),
		draft,
		row.pr.Author.Login,
	)
}

// ------------------------------------------------------------
// View
// ------------------------------------------------------------

// actionRowView renders the single-line row under the title. It's
// always present (chrome math is simpler that way) and shows one of:
//   - the spinner + what is in flight ("opening #42…") while children run
//   - the loading spinner + "loading PRs…" while the initial fetch is pending
//   - the search input when the user is typing
//   - the filter chip when a filter is applied but the input is blurred
//   - the last action's failure, or a fetch failure while the list is stale
//   - the counts summary otherwise
//
// Never more than one visible line — length may exceed width and be
// truncated by the terminal; that's acceptable for a status row.
func (m model) actionRowView() string {
	switch {
	case len(m.inflight) > 0:
		labels := make([]string, 0, len(m.inflight))
		for _, pr := range slices.Sorted(maps.Keys(m.inflight)) {
			labels = append(labels, m.inflight[pr])
		}
		return m.spinner.View() + " " + styleDim.Render(strings.Join(labels, "  "))
	case !m.ready && m.err == nil:
		return m.spinner.View() + " " + styleDim.Render("loading "+m.noun()+"…")
	case m.refreshing:
		return m.spinner.View() + " " + styleDim.Render("refreshing…")
	case m.search.Focused():
		return styleSearchLabel.Render("search /") + m.search.View()
	case m.search.Value() != "":
		return styleSearchLabel.Render("filter /"+m.search.Value()) +
			styleDim.Render("   [/] edit   [esc] clear")
	case m.notice != nil:
		return styleChangesReqd.Render("error: " + m.notice.Error())
	case m.err != nil && m.hasData():
		// The list is still the last good fetch; say so instead of
		// replacing it (offline with a warm cache is the common case).
		return styleChangesReqd.Render("error: " + m.err.Error())
	default:
		return m.countsSummary()
	}
}

// hasData reports whether there is a list to show — from a fetch or
// the cache.
func (m model) hasData() bool {
	switch m.kind {
	case "issue":
		return len(m.issues) > 0
	case "project":
		return len(m.projects) > 0
	}
	return len(m.prs) > 0 || len(m.merged) > 0
}

// noun is what the list holds, plural: PRs, issues or projects.
func (m model) noun() string {
	switch m.kind {
	case "issue":
		return "issues"
	case "project":
		return "projects"
	}
	return "PRs"
}

// countsSummary is the idle-state action row content: a compact
// count-per-group line so a glance tells you today's shape.
func (m model) countsSummary() string {
	switch m.kind {
	case "issue":
		return m.issueCountsSummary()
	case "project":
		return m.projectCountsSummary()
	}
	if m.me == "" || len(m.prs) == 0 && len(m.merged) == 0 {
		return styleDim.Render(fmt.Sprintf("%d open", len(m.prs)))
	}
	counts := map[ReviewStatus]int{}
	for _, pr := range m.prs {
		counts[pr.MyReviewStatus(m.me)]++
	}
	return styleDim.Render(fmt.Sprintf(
		"%d open · %d todo · %d you · %d author · %d approved · %d merged (%s)",
		len(m.prs),
		counts[StatusTodo],
		counts[StatusWaitingForYou],
		counts[StatusWaitingForAuthor],
		counts[StatusApproved],
		len(m.merged),
		mergedWindowLabel,
	))
}

// titleLine renders the header row: `owl · <repo>` left-aligned,
// `updated Xm ago` right-aligned, padded to fill m.width. Timestamp
// is omitted before the first fetch completes (lastFetched is zero).
func (m model) titleLine(repo string) string {
	left := styleHeader.Render(fmt.Sprintf("owl · %s", repo))
	switch m.kind {
	case "issue":
		left = styleHeader.Render(fmt.Sprintf("owl · issues · %s", repo))
	case "project":
		left = styleHeader.Render(fmt.Sprintf("owl · projects · %s", repo))
	}
	right := ""
	if !m.lastFetched.IsZero() {
		d := time.Since(m.lastFetched)
		switch {
		case d < time.Minute:
			right = styleDim.Render("updated just now")
		case d < time.Hour:
			right = styleDim.Render(fmt.Sprintf("updated %dm ago", int(d.Minutes())))
		case d < 24*time.Hour:
			right = styleDim.Render(fmt.Sprintf("updated %dh ago", int(d.Hours())))
		default:
			right = styleDim.Render(fmt.Sprintf("updated %dd ago", int(d.Hours()/24)))
		}
	}
	if m.width == 0 {
		if right == "" {
			return left
		}
		return left + "  " + right
	}
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return left + strings.Repeat(" ", pad) + right
}

// helpModalView renders a centered, bordered box with the full key
// legend AND the color/glyph conventions. Dismissed by any key.
func (m model) helpModalView() string {
	title := styleHeader.Render("owl · help")

	legend := m.legend()

	keys := styleHeader.Render("Keys") + "\n" +
		m.help.FullHelpView(m.keys.FullHelp())

	dismiss := styleDim.Render("press any key to dismiss   (q quits)")

	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		legend,
		"",
		keys,
		"",
		dismiss,
	)

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 3).
		Render(body)

	if m.width == 0 || m.height == 0 {
		return box
	}
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

// legend explains the glyphs of the list's rows.
func (m model) legend() string {
	switch m.kind {
	case "issue":
		return m.issueLegend()
	case "project":
		return m.projectLegend()
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   worktree present for pr-<N>[-…] branch", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude working — actively processing a turn", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s   Claude blocked — waiting on you (permission, question, plan approval)", styleClaudeBlocked.Render("©")),
		fmt.Sprintf("  %s  Claude done — unread (result to view; ack by focusing the window)", styleClaudeDone.Render("©")+styleClaudeDone.Render("*")),
		fmt.Sprintf("  %s   Claude idle — finished and seen (session still available)", styleClaudeDone.Render("©")),
		fmt.Sprintf("  %s   Claude session — no state set (fresh window)", styleClaudeNeutral.Render("©")),
		fmt.Sprintf("  %s   I approved this PR (current verdict)", styleApproved.Render("✓")),
		fmt.Sprintf("  %s   I engaged — commented or requested changes, no approval", styleDim.Render("·")),
		fmt.Sprintf("  %s %s the author pushed after that review — it no longer covers the head", styleReviewStale.Render("✓"), styleReviewStale.Render("·")),
		fmt.Sprintf("  %s   changes requested by any reviewer (PR blocked)", styleChangesReqd.Render("⚠")),
	)
}

// View wraps the rendered frame with what used to be program options:
// the alternate screen and focus reporting (auto-refresh on focus).
// The search input draws its own cursor.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

func (m model) render() string {
	if m.showHelp {
		return m.helpModalView()
	}

	var b strings.Builder

	repo := m.repo
	if repo == "" {
		repo = "(not in a GitHub repo)"
	}
	b.WriteString(m.titleLine(repo) + "\n")
	b.WriteString(m.actionRowView() + "\n")
	b.WriteString(strings.Repeat("─", clampInt(m.width, 20, 200)) + "\n")

	if m.err != nil && !m.hasData() {
		b.WriteString(fmt.Sprintf("\nerror: %v\n", m.err))
		b.WriteString("\n" + m.help.View(m.keys))
		return b.String()
	}

	if !m.ready {
		// Loading state is already shown in the action row (spinner + text).
		// Leave the body blank so the eye stays where the movement is.
		return b.String()
	}

	rows := m.visibleRows()
	if len(rows) == 0 {
		switch {
		case m.search.Value() != "":
			b.WriteString(fmt.Sprintf("\nno %s match /%s\n", m.noun(), m.search.Value()))
		case m.kind == "project":
			b.WriteString("\nno open projects you work in.\n")
		case m.kind == "issue":
			b.WriteString("\nno open issues assigned to you.\n")
		default:
			b.WriteString("\nno PRs need your review.\n")
		}
		b.WriteString("\n" + m.help.View(m.keys))
		return b.String()
	}

	b.WriteString(m.list.View())
	b.WriteString("\n\n" + m.help.View(m.keys))
	return b.String()
}

// ------------------------------------------------------------
// Badges & helpers
// ------------------------------------------------------------

// badges renders a fixed-width block of colored state glyphs.
//
//	Slot 1  ⎇   worktree present — or the spinner while a child starts,
//	            opens or closes this PR's workspace
//	Slot 2  ©*  Claude session — the trailing `*` (or space) is an unread marker:
//	              ©   yellow = working (actively processing)
//	              ©   amber  = blocked (waiting on permission / question / plan)
//	              ©*  green  = done, unread (result to view)
//	              ©   green  = idle (done and acknowledged)
//	              ©   gray   = session exists, no state set (fresh window)
//	Slot 3  ✓   I approved (green) — my current verdict is APPROVED
//	            ·  engaged (dim) — I commented/CR'd but did not approve
//	            either in amber when the author pushed after that review:
//	            the glyph is what I did, the colour whether it still
//	            covers the head (same grammar as the © slot)
//	Slot 4  ⚠   any reviewer currently requesting changes (PR blocked)
//
// Absent = single space so column alignment stays. Slot 2 is always
// 2 cells wide (glyph + `*`|space) because of the unread marker.
func badges(ls LocalState, starting string, iApproved, iEngaged, stale, hasCR bool) string {
	var parts []string

	switch {
	case starting != "":
		parts = append(parts, starting)
	case ls.Worktree != "":
		parts = append(parts, styleWorktree.Render("⎇"))
	default:
		parts = append(parts, " ")
	}

	// Claude slot: © + unread marker (`*` for done, else space).
	switch ls.ClaudeState {
	case agentWorking:
		parts = append(parts, styleClaudeWorking.Render("©")+" ")
	case agentBlocked:
		parts = append(parts, styleClaudeBlocked.Render("©")+" ")
	case agentDone:
		parts = append(parts, styleClaudeDone.Render("©")+styleClaudeDone.Render("*"))
	case agentIdle:
		parts = append(parts, styleClaudeDone.Render("©")+" ")
	default:
		if ls.Window != "" {
			parts = append(parts, styleClaudeNeutral.Render("©")+" ")
		} else {
			parts = append(parts, "  ")
		}
	}

	switch {
	case iApproved && stale:
		parts = append(parts, styleReviewStale.Render("✓"))
	case iApproved:
		parts = append(parts, styleApproved.Render("✓"))
	case iEngaged && stale:
		parts = append(parts, styleReviewStale.Render("·"))
	case iEngaged:
		parts = append(parts, styleDim.Render("·"))
	default:
		parts = append(parts, " ")
	}

	if hasCR {
		parts = append(parts, styleChangesReqd.Render("⚠"))
	} else {
		parts = append(parts, " ")
	}

	return strings.Join(parts, " ")
}

// trim shortens s to at most n runes, replacing the tail with an
// ellipsis if it was cut. Runes, not bytes: slicing a title on a byte
// boundary inside "—" or an emoji emits a broken UTF-8 sequence.
func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// relativeAge formats an RFC3339 timestamp as "now", "5m", "3h", "2d"
// or "3w" — three cells at most.
func relativeAge(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ------------------------------------------------------------
// Entry
// ------------------------------------------------------------

// version is set by the release build (-ldflags "-X main.version=…");
// `go install …@vX.Y.Z` builds report the module version instead.
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// about is what bare owl says: what it is, before what it takes.
const about = `owl — the pull requests waiting for your review, and the issues waiting
for your hands, one keystroke from any terminal.

Each becomes a workspace when you want it: a git worktree on its branch,
a window in your multiplexer — tmux, herdr or cmux — and Claude Code
inside it, on the review or on the feature. owl stores nothing of its
own: the branch carries the ticket, the pull request carries the review,
the window carries the agent, and the lists read all of it back from
GitHub, Linear and the multiplexer. Two skills give the agent its manners
— review never posts without you, feature never pushes without you. What
no ticket names yet, you hoot.
`

const usage = `usage: owl [--config FILE] [--mux tmux|herdr|cmux] [<noun> [command]]
       owl                              this introduction
       owl pr                           the PR list (run inside a git repo, or with default_repo set)
       owl pr open <N> [--prompt TEXT]  open (or focus) the review of PR N
       owl pr start <N> [--prompt TEXT] the same without going there: no window selection, no after_open
       owl pr close [--force] [<N>]     remove PR N's worktree, branch and window; --force discards uncommitted changes
       owl issue                        the issues assigned to you, from Linear
       owl issue open <KEY> [--prompt TEXT]  open (or focus) the feature workspace of issue KEY
       owl issue start <KEY> [--prompt TEXT] the same without going there
       owl issue close [--force] [<KEY>]     remove the feature's worktree, local branch and window
       owl issue new <title…>           file an issue in linear.team, assigned to you
       owl project                      the projects you work in, from Linear
       owl hoot <title…>                the same, from the owl
       owl config init | path
       owl --version`

// usageError is a bad invocation: the message is printed with the
// usage text and the process exits 64 (EX_USAGE).
type usageError string

func (e usageError) Error() string { return string(e) }

func main() {
	args, err := globalOptions(os.Args[1:])
	exitOn(err)
	// Bare owl says what it is; the lists are behind their nouns, so a
	// verb never has to guess which kind of thing an id names.
	if len(args) == 0 {
		fmt.Print(about)
		fmt.Println()
		fmt.Println(usage)
		return
	}
	// The noun is the scope. A verb without one is refused with the
	// form it takes.
	if len(args) > 0 {
		switch args[0] {
		case "config":
			exitOn(runConfig(args[1:], os.Stdout))
			return
		case "--version", "version":
			fmt.Println("owl", versionString())
			return
		case "--help", "-h", "help":
			fmt.Println(usage)
			return
		case "pr":
			args = args[1:]
			if len(args) > 0 {
				switch args[0] {
				case "open", "start", "close":
				default:
					exitOn(usageError("pr: unknown command " + args[0]))
				}
			}
		case "issue":
			if len(args) > 1 {
				switch args[1] {
				case "open", "start", "close", "new":
				default:
					exitOn(usageError("issue: unknown command " + args[1]))
				}
			}
		case "project":
			if len(args) > 1 {
				exitOn(usageError("project: unknown command " + args[1]))
			}
		case "hoot":
		case "open", "start", "close":
			exitOn(usageError(args[0] + " is a pr command: owl pr " + args[0]))
		default:
			exitOn(usageError("unknown command " + args[0]))
		}
	}

	cfg, err := loadConfig()
	exitOn(err)
	if muxOverride != "" {
		cfg.Mux = muxOverride
	}
	exitOn(enterDefaultRepo(cfg.DefaultRepo))
	switch {
	case len(args) > 0 && args[0] == "issue":
		if len(args) == 1 && term.IsTerminal(os.Stdout.Fd()) {
			exitOn(newWindows(cfg, features).Ping())
			tracker := newTracker(cfg, func(text string) {
				fmt.Fprintln(os.Stderr, text)
				newWindows(cfg, features).Notify(text)
			})
			runTUI(initialIssueModel(cfg, tracker))
			return
		}
		exitOn(runIssue(cfg, args[1:], os.Stdout))
		return
	case len(args) > 0 && args[0] == "project":
		if len(args) == 1 && term.IsTerminal(os.Stdout.Fd()) {
			exitOn(newWindows(cfg, features).Ping())
			tracker := newTracker(cfg, func(text string) {
				fmt.Fprintln(os.Stderr, text)
				newWindows(cfg, features).Notify(text)
			})
			runTUI(initialProjectModel(cfg, tracker))
			return
		}
		exitOn(runProject(cfg, args[1:], os.Stdout))
		return
	case len(args) > 0 && args[0] == "hoot":
		exitOn(runIssue(cfg, append([]string{"new"}, args[1:]...), os.Stdout))
		return
	case len(args) == 0:
		// Before the list: states read from a tainted multiplexer never
		// change, and the failure would surface on Enter, an hour in.
		exitOn(newWindows(cfg, reviews).Ping())
		runTUI(initialModel(cfg))
	case args[0] == "open":
		err = runOpen(cfg, args[1:], os.Stdout, true)
	case args[0] == "start":
		err = runOpen(cfg, args[1:], os.Stdout, false)
	case args[0] == "close":
		err = runClose(cfg, args[1:], os.Stdout)
	}
	exitOn(err)
}

// runTUI runs a list until it quits, keeps its cursor for the next
// start, and prints what an open left for the terminal behind it.
func runTUI(m model) {
	final, err := tea.NewProgram(m).Run()
	exitOn(err)
	fm := final.(model)
	fm.persistCache() // the cursor row, for the next start
	if fm.farewell != "" {
		fmt.Println(fm.farewell)
	}
}

// muxOverride is the --mux flag, when given: the multiplexer to use
// whatever the config says.
var muxOverride string

// globalOptions takes --config FILE and --mux KIND off the front of
// the arguments — they apply to every command — and returns the rest.
func globalOptions(args []string) ([]string, error) {
	for len(args) > 0 {
		name, value, joined := strings.Cut(args[0], "=")
		if name != "--config" && name != "--mux" {
			break
		}
		if !joined {
			if len(args) < 2 {
				return nil, usageError(name + " needs a value")
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		switch name {
		case "--config":
			configOverride = value
		case "--mux":
			switch value {
			case "tmux", "herdr", "cmux":
				muxOverride = value
			default:
				return nil, usageError("--mux must be tmux, herdr or cmux, got " + value)
			}
		}
	}
	return args, nil
}

// globalArgs is what a child owl needs in front of its command to
// see the same --config and --mux as this process: a child parses its
// own arguments.
func globalArgs() []string {
	var args []string
	if configOverride != "" {
		args = append(args, "--config", configOverride)
	}
	if muxOverride != "" {
		args = append(args, "--mux", muxOverride)
	}
	return args
}

// enterDefaultRepo changes into `default_repo` when the working
// directory isn't inside a git repo, so owl can be launched from
// anywhere (a hotkey, a popup) and still act on the configured repo.
func enterDefaultRepo(defaultRepo string) error {
	if defaultRepo == "" || exec.Command("git", "rev-parse", "--git-dir").Run() == nil {
		return nil
	}
	if err := os.Chdir(defaultRepo); err != nil {
		return fmt.Errorf("default_repo: %w", err)
	}
	return nil
}

// exitOn prints err and exits: 64 for a usage error (with the usage
// text), 2 when `close` found nothing to do, 1 otherwise.
func exitOn(err error) {
	if err == nil {
		return
	}
	// Started by the TUI, which may already have quit (on_open: quit):
	// the failure goes to the multiplexer the popup was in — before
	// stderr, which may be a broken pipe by now and would end the
	// process.
	if mx, ok := windowsByKind(os.Getenv("OWL_MUX")); ok {
		mx.Notify(err.Error())
	}
	fmt.Fprintf(os.Stderr, "owl: %v\n", err)
	var ue usageError
	var nothing nothingToCloseError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(64)
	case errors.As(err, &nothing):
		os.Exit(2)
	}
	os.Exit(1)
}
