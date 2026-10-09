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
	"context"
	"strconv"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stefanahman/mux"
)

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
	// mine marks a PR row as one of yours, in the mine pane: the badges
	// and the keys it answers to are that pane's, not the review list's.
	mine    bool
	issue   *Issue
	project *Project
}

// header reports whether the row is a section title.
func (r visibleRow) header() bool { return r.pr == nil && r.issue == nil && r.project == nil }

// noun is the owl command that acts on this row — `owl <noun> open`.
// It follows the row and not the list, because a drilled project list
// shows issue rows, and `owl project open BAR-4079` is not a thing.
func (r visibleRow) noun() string {
	switch {
	case r.issue != nil:
		return "issue"
	case r.project != nil:
		return "project"
	}
	return "pr"
}

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
	repo    string // owner/name on GitHub, of the repo you are standing in
	repoDir string // the repository's main working tree; "" outside a repo
	// here narrows the list to m.repo even where owners would span more,
	// for the times you want this repo and not the whole desk.
	here bool
	// onlyWorkspace is the Linear workspace the issue and project lists
	// show, by its configured name; "" where only one is configured and
	// there is nothing to choose. Tab steps through them, and the cache
	// remembers where it stopped.
	onlyWorkspace string
	// paneChosen is set once Tab has been pressed. Until then the list
	// may land the focus on whichever pane has rows; after it, the
	// reader has said which pane they want and a fetch landing must not
	// move them.
	paneChosen bool
	// prsAnswered and mineAnswered say which of the two fetches have
	// come back. `ready` is set by whichever lands first, so it cannot
	// tell an empty pane from one still being fetched — and the focus
	// must not be decided on the difference.
	prsAnswered  bool
	mineAnswered bool
	me           string
	prs          []PR
	merged       []PR
	mine         []PR // your own open PRs: the mine pane
	mineMerged   []PR // your own PRs merged inside merged_window
	// mineFocus says the mine pane has the cursor and the keys. Only
	// the PR list has two panes; the other lists leave it false.
	// otherCursor holds the row the unfocused pane was left on, and the
	// two swap when Tab moves the focus, so every cursor and row
	// function keeps reading m.cursor and needs to know nothing about
	// panes.
	mineFocus   bool
	otherCursor int
	// ctx ends when the list does, and is what the multiplexer's watch
	// is started on. nil outside runTUI — a model in a test or a
	// one-shot command starts no watch.
	ctx context.Context
	// stateDriver is the multiplexer this list reads agent state
	// through, kept for the life of the list so that a watch started on
	// it survives; nil under tmux, and until the watch command lands.
	// stateSignal wakes the list when the multiplexer says a state
	// changed, and is nil where the multiplexer has none.
	stateDriver mux.Driver
	stateSignal <-chan struct{}

	// contentHeight is the rows' share of the screen, chrome removed.
	// With two panes the viewport gets part of it and the rest is drawn
	// beneath; both come from this one number.
	contentHeight int

	issues     []Issue
	doneIssues []Issue // completed inside doneWindow; the Done section
	// cancelledIssues are the ones cancelled inside cancelledWindow, in
	// a section of their own — cancelling is not finishing.
	cancelledIssues []Issue
	projects        []Project // the project list's rows
	doneProjects    []Project // completed inside doneProjectWindow; the Done section
	// drill is the project whose issues are showing in place of the
	// project list, and drillIssues are that project's — everyone's, not
	// only the user's. nil when the project list itself is showing.
	drill       *Project
	drillIssues []Issue
	drillCursor int             // the project row to come back to
	issuePRs    map[string][]PR // the repo's open PRs by issue key, for the issue rows
	localState  map[string]LocalState
	// projectStates is the project state file, so a project's row finds
	// its workspace under the name of its first open as well as its
	// current one. Read with the local state, since an open in between
	// is what adds to it.
	projectStates map[string]projectState

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
// scope is the repository qualifier this model's PR searches carry:
// the configured owners, or the one repo when `--here` asked for it or
// no owners are configured. "" only outside a repo with no owners,
// where there is nothing to search and the caller says so.
func (m model) scope() string { return m.cfg.PR.Scope(m.repo, m.here) }

func initialModel(cfg Config, here bool) model {
	repo := currentRepo(cfg.Remote)
	m := newModel(cfg, repo, loadCache(prCacheKey(cfg.PR, repo, here)))
	m.here = here
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
	m.kind, m.sc, m.tracker = "project", projects, tracker
	m.search.Placeholder = "project name"
	m.search.CharLimit = 64
	m.keys = newKeyMap(cfg.Keys, nil)
	m.keys.Enter.SetHelp(m.keys.Enter.Help().Key, "open project conversation")
	m.keys.Start.SetHelp(m.keys.Start.Help().Key, "start (stay)")
	m.keys.Browser.SetHelp(m.keys.Browser.Help().Key, "open project in Linear")
	m.keys.Yank.SetHelp(m.keys.Yank.Help().Key, "yank project name")
	m.keys.Cleanup.SetHelp(m.keys.Cleanup.Help().Key, "close the project workspace")
	m.labelWorkspaceKey()
	m.onlyWorkspace = m.configuredWorkspace("")
	if cache != nil {
		m.projects = cache.Projects
		m.doneProjects = cache.DoneProjects
		m.issues = cache.Issues
		// Before the cursor: the row it names is a row of this
		// workspace's list, not of another's.
		m.onlyWorkspace = m.configuredWorkspace(cache.Workspace)
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
	m.labelWorkspaceKey()
	m.onlyWorkspace = m.configuredWorkspace("")
	if cache != nil {
		m.issues = cache.Issues
		m.doneIssues = cache.DoneIssues
		m.cancelledIssues = cache.CancelledIssues
		m.issuePRs = byIssueKey(cache.IssuePRs)
		m.onlyWorkspace = m.configuredWorkspace(cache.Workspace)
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
		m.mine = cache.Mine
		m.mineMerged = cache.MineMerged
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
		localTick(false), // no watch yet; the next tick knows
		startWatch(m.ctx, m.cfg),
		m.spinner.Tick,
		textinput.Blink,
		tea.RequestBackgroundColor,
	)...)
}

// fetches are the list's remote fetches, for Init, refresh and focus.
func (m model) fetches() []tea.Cmd {
	switch m.kind {
	case "issue":
		return []tea.Cmd{m.fetchIssues, m.fetchIssuePRs, m.fetchDone, m.fetchCancelled}
	case "project":
		// The issues too: a row says how much of the project is yours.
		return []tea.Cmd{m.fetchProjects, m.fetchIssues, m.fetchDoneProjects}
	}
	// No fetch for the login: the PR search answers `viewer { login }`
	// in the same request, and a `gh api user` of its own was half a
	// second of round trip on every refresh for one string that never
	// changes.
	return []tea.Cmd{m.fetchPRs, m.fetchMerged, m.fetchMine}
}

// persistCache writes the list's snapshot and the cursor row to its
// cache file. Called after a fetch lands, and once more on exit for
// the cursor, so the next popup startup has warm data and the same
// row selected. All errors swallowed inside the writer — cache is
// best-effort.
func (m model) persistCache() {
	if m.kind == "project" {
		saveProjectCache(projectCacheFile{Projects: m.projects, DoneProjects: m.doneProjects, Issues: m.issues, FetchedAt: m.lastFetched, Cursor: m.cursor, Workspace: m.onlyWorkspace})
		return
	}
	if m.kind == "issue" {
		saveIssueCache(issueCacheFile{Issues: m.issues, DoneIssues: m.doneIssues, CancelledIssues: m.cancelledIssues, IssuePRs: flattenPRs(m.issuePRs), FetchedAt: m.lastFetched, Cursor: m.cursor, Workspace: m.onlyWorkspace})
		return
	}
	saveCache(prCacheKey(m.cfg.PR, m.repo, m.here), cacheFile{
		Prs:        m.prs,
		Merged:     m.merged,
		Mine:       m.mine,
		MineMerged: m.mineMerged,
		Me:         m.me,
		FetchedAt:  m.lastFetched,
		Cursor:     m.cursor,
	})
}
