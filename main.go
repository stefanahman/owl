// pr-owl — TUI overview of PRs where you're a reviewer, with local
// state (worktree, tmux window in the pr-reviews session, Claude
// activity) and review involvement (approved / engaged / any-CR)
// overlaid.
//
// Widgets used, all docs-endorsed from charmbracelet/bubbles:
//
//	bubbles/viewport  scroll container (slice-based rendering, table pattern)
//	bubbles/textinput search field
//	bubbles/spinner   loading indicator
//	bubbles/key       key bindings (rebindable, drive the help view)
//	bubbles/help      auto-generates key legend from the keymap
//
// Data layer (gh.go, local.go) is untouched by the refactor.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ------------------------------------------------------------
// Messages
// ------------------------------------------------------------

type prsMsg []PR
type mergedMsg []PR
type userMsg string // authenticated user login

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

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
	Feedback key.Binding
	Browser  key.Binding
	Linear   key.Binding
	Yank     key.Binding
	Next     key.Binding
	Cleanup  key.Binding
	Search   key.Binding
	Cancel   key.Binding
	Refresh  key.Binding
	Help     key.Binding
	Quit     key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Up:       key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Home:     key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		End:      key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		PageUp:   key.NewBinding(key.WithKeys("pgup", "ctrl+u"), key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(key.WithKeys("pgdown", "ctrl+d"), key.WithHelp("pgdn", "page down")),
		Enter:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "open review")),
		Feedback: key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "check feedback")),
		Browser:  key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "open PR in browser")),
		Linear:   key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "open Linear ticket")),
		Yank:     key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "yank PR URL")),
		Next:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next attention-needed")),
		Cleanup:  key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "clean up worktree")),
		Search:   key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Cancel:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel/clear")),
		Refresh:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// ShortHelp drives the footer legend. FullHelp is rendered inside the
// `?` modal (helpModalView) via help.FullHelpView.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Next, k.Enter, k.Feedback, k.Browser, k.Search, k.Help, k.Quit}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Next, k.Home, k.End, k.PageUp, k.PageDown},
		{k.Enter, k.Feedback, k.Browser, k.Linear, k.Yank, k.Cleanup},
		{k.Search, k.Cancel, k.Refresh, k.Help, k.Quit},
	}
}

// ------------------------------------------------------------
// Styles
// ------------------------------------------------------------

var (
	styleWorktree      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // blue    ⎇  worktree present
	styleClaudeAmber   = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))     // amber   ©  Claude halted (blocked)
	styleClaudeWorking = lipgloss.NewStyle().Foreground(lipgloss.Color("#dbbc7f")) // yellow  ©  Claude actively processing
	styleClaudeGreen   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))      // green   ©  Claude finished (unread + `*`, or read without)
	styleClaudeNeutral = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))     // gray    ©  session exists, no state (fresh window)
	styleApproved      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green   ✓  I approved (current verdict)
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

// ------------------------------------------------------------
// Row model
// ------------------------------------------------------------

// visibleRow is either a section header (pr == nil) or a PR row.
type visibleRow struct {
	sectionTitle string
	sectionStyle lipgloss.Style
	pr           *PR
	status       ReviewStatus
	merged       bool
}

// ------------------------------------------------------------
// Model
// ------------------------------------------------------------

type model struct {
	// domain data
	repo       string
	me         string
	prs        []PR
	merged     []PR
	localState map[string]LocalState

	// load state
	prsReady    bool
	err         error
	lastFetched time.Time // set when prsMsg lands; drives "updated X ago"

	// UI state
	cursor int // index into the current visibleRows() output

	// widgets
	keys    keyMap
	help    help.Model
	search  textinput.Model
	list    viewport.Model
	spinner spinner.Model

	showHelp bool // true → full-screen help modal covers the list

	width, height int

	// initCmds overrides the default Init() batch. Tests set this to an
	// empty slice to suppress the shell-out fetches; production leaves
	// it nil so the real fetches fire.
	initCmds []tea.Cmd
}

func initialModel() model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "PR number"
	ti.CharLimit = 16
	// Note: bubbles/textinput.Validate is display-only (sets m.Err,
	// doesn't reject). Digit-only filtering lives upstream in
	// handleKey — non-digit runes are dropped before reaching textinput.

	sp := spinner.New()
	sp.Style = styleDim

	m := model{
		repo:    currentRepo(),
		keys:    newKeyMap(),
		help:    help.New(),
		search:  ti,
		list:    viewport.New(0, 0),
		spinner: sp,
	}
	// Cache-first: if a previous session left a cache for this repo,
	// seed the state so the popup renders instantly. The live fetches
	// still fire in Init and overwrite the state when they land — the
	// "updated Xm ago" indicator shows freshness so the user sees when
	// they're looking at stale data.
	if c := loadCache(m.repo); c != nil {
		m.prs = c.Prs
		m.merged = c.Merged
		m.me = c.Me
		m.prsReady = true
		m.lastFetched = c.FetchedAt
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.initCmds != nil {
		return tea.Batch(m.initCmds...)
	}
	return tea.Batch(
		fetchPRs, fetchLocal, fetchMerged, fetchUser,
		m.spinner.Tick,
		textinput.Blink,
	)
}

func fetchUser() tea.Msg { return userMsg(currentUser()) }

// persistCache writes the current prs/merged/me snapshot to the
// per-repo cache file. Called after any of the three land so the
// next popup startup has warm data. All errors swallowed inside
// saveCache — cache is best-effort.
func (m model) persistCache() {
	saveCache(m.repo, cacheFile{
		Prs:       m.prs,
		Merged:    m.merged,
		Me:        m.me,
		FetchedAt: m.lastFetched,
	})
}

// launchPRReview opens (or focuses) the review workspace for the given
// PR — the pr-review script is idempotent. Detached because pr-owl
// quits immediately after firing it (popup closes); pr-review keeps
// running in its own session.
func launchPRReview(prNumber int) tea.Cmd {
	return launchDetached("pr-review", strconv.Itoa(prNumber))
}

// launchPRReviewWithPrompt is like launchPRReview but passes an
// explicit prompt to pr-review as its second arg. pr-review submits
// that prompt to Claude on load — via `claude "$prompt"` (fresh) or
// `claude -c "$prompt"` (resume). Used by the `f` shortcut when the
// session isn't running but a prior conversation exists on disk:
// one command spawns Ghostty → tmux → Claude, resumes, and submits
// the check-feedback prompt, no timing race.
func launchPRReviewWithPrompt(prNumber int, prompt string) tea.Cmd {
	return launchDetached("pr-review", strconv.Itoa(prNumber), prompt)
}

// launchDetached runs an eden-bin script in its OWN session (Setsid)
// and returns immediately without waiting. Needed because pr-owl often
// runs inside a tmux popup; when pr-owl quits, tmux closes the popup
// and sends SIGHUP to processes still in that popup's session — which
// would kill the pr-review subprocess mid-flight. Setsid puts it in a
// fresh session unreachable by the popup's SIGHUP.
//
// Trade: no auto-refresh of local state after the script finishes
// (which is fine — the callers here always pair with tea.Quit).
func launchDetached(name string, args ...string) tea.Cmd {
	return func() tea.Msg {
		home, err := os.UserHomeDir()
		if err != nil {
			return errMsg{fmt.Errorf("resolve $HOME: %w", err)}
		}
		cmd := exec.Command(home+"/.eden/bin/"+name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return errMsg{fmt.Errorf("start %s: %w", name, err)}
		}
		// Release so the parent doesn't need to reap on exit.
		_ = cmd.Process.Release()
		return nil
	}
}

// hasPriorConversation reports whether Claude has any recorded session
// jsonl for this PR — surviving worktree cleanup. Scans
// ~/.claude/projects/ for a matching encoded-path dir and checks for
// at least one .jsonl inside.
func hasPriorConversation(prNumber int) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(home + "/.claude/projects")
	if err != nil {
		return false
	}
	needle := fmt.Sprintf("-pr-%d", prNumber)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		loc := strings.Index(name, needle)
		if loc == -1 {
			continue
		}
		// Ensure the digits end at `-` or end-of-string (exact match).
		end := loc + len(needle)
		if end < len(name) && name[end] != '-' {
			continue
		}
		sub, err := os.ReadDir(home + "/.claude/projects/" + name)
		if err != nil {
			continue
		}
		for _, f := range sub {
			if strings.HasSuffix(f.Name(), ".jsonl") {
				return true
			}
		}
	}
	return false
}

// cleanupPRWorktree tears down the review workspace for the given PR:
// removes the .worktrees.local/pr-<N>-* worktree, kills the tmux
// session (Claude conversation state persists on disk), closes any
// pr-<N> Ghostty window. Safety-checked upstream: pr-review-done
// refuses when the worktree has uncommitted work.
func cleanupPRWorktree(prNumber int) tea.Cmd {
	return runEdenScript("pr-review-done", prNumber)
}

// checkFeedbackPrompt is the canned message sent to a PR's Claude
// session by the `f` binding. Two-pass self-critique + RESOLVED
// taxonomy, calm tenor (research: encouraging language increases
// deliberation; desperation language causes shortcuts). No
// `ultrathink` — deep enough via the two-pass structure without
// paying max-thinking latency on every trigger.
const checkFeedbackPrompt = "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict."

// sendCheckFeedback dispatches the check-feedback prompt into the
// pr-reviews tmux window for this PR, selects that window, and focuses
// the pr-reviews Ghostty window so the user sees Claude respond.
//
// `window` is the tmux window name (e.g. `pr-4141-fix-ci-…`) — under
// the consolidated model, LocalState.Session carries this rather than
// a tmux session name.
func sendCheckFeedback(window string, prNumber int) tea.Cmd {
	return func() tea.Msg {
		target := reviewsSessionName + ":" + window
		// Select the window so it's what the Ghostty client shows.
		if err := exec.Command("tmux", "select-window", "-t", target).Run(); err != nil {
			return errMsg{fmt.Errorf("select-window %s: %w", target, err)}
		}
		// Literal send for the prompt (arbitrary text, no key
		// interpretation), then Enter to submit.
		if err := exec.Command("tmux", "send-keys", "-t", target, "-l", checkFeedbackPrompt).Run(); err != nil {
			return errMsg{fmt.Errorf("send-keys prompt: %w", err)}
		}
		if err := exec.Command("tmux", "send-keys", "-t", target, "Enter").Run(); err != nil {
			return errMsg{fmt.Errorf("send-keys Enter: %w", err)}
		}
		focusReviewsWindow()
		return nil
	}
}

// focusReviewsWindow brings the single `pr-reviews` Ghostty window to
// the front via yabai. Best-effort — no error surface if yabai isn't
// running or the window doesn't exist (session may be detached).
//
// Prefix match on the title (not exact) because some shell prompts
// OSC-update the terminal title after Ghostty's --title=pr-reviews.
func focusReviewsWindow() {
	out, err := exec.Command("sh", "-c",
		`yabai -m query --windows | jq -r '.[] | select(.app=="Ghostty" and ((.title // "") == "pr-reviews" or ((.title // "") | startswith("pr-reviews ") or startswith("pr-reviews:")))) | .id' | head -1`).Output()
	if err != nil {
		return
	}
	wid := strings.TrimSpace(string(out))
	if wid == "" {
		return
	}
	_ = exec.Command("yabai", "-m", "window", "--focus", wid).Run()
}

// openPRInBrowser opens the PR's GitHub page in the default browser
// via `gh pr view --web`.
func openPRInBrowser(prNumber int) tea.Cmd {
	return func() tea.Msg {
		if err := exec.Command("gh", "pr", "view", "--web", strconv.Itoa(prNumber)).Run(); err != nil {
			return errMsg{fmt.Errorf("gh pr view --web %d: %w", prNumber, err)}
		}
		return nil
	}
}

// linearIDRe matches Linear ticket identifiers of the form `BAR-1234`
// anywhere in a PR title, body, or branch name. Bardo team convention
// is the `BAR-` prefix; other prefixes would need a config knob to
// support cleanly (skipped for v1).
var linearIDRe = regexp.MustCompile(`\bBAR-\d+\b`)

// openLinearForPR parses a Linear ticket ID from the PR's title, body,
// and branch name (in that order) and opens the corresponding Linear
// URL in the default browser. No-op when no ID is found.
func openLinearForPR(pr *PR) tea.Cmd {
	return func() tea.Msg {
		haystack := pr.Title + "\n" + pr.Body + "\n" + pr.HeadRefName
		id := linearIDRe.FindString(haystack)
		if id == "" {
			return nil
		}
		url := "https://linear.app/bardo-technology/issue/" + id
		if err := exec.Command("open", url).Run(); err != nil {
			return errMsg{fmt.Errorf("open %s: %w", url, err)}
		}
		return nil
	}
}

// yankPRURL copies the PR's GitHub URL to the macOS clipboard via
// pbcopy. URL is constructed from the current repo + PR number, which
// is cheaper than a separate `gh pr view --json url` roundtrip.
func yankPRURL(repo string, prNumber int) tea.Cmd {
	return func() tea.Msg {
		if repo == "" {
			return errMsg{fmt.Errorf("yank: no repo detected")}
		}
		url := fmt.Sprintf("https://github.com/%s/pull/%d", repo, prNumber)
		cmd := exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(url)
		if err := cmd.Run(); err != nil {
			return errMsg{fmt.Errorf("pbcopy: %w", err)}
		}
		return nil
	}
}

// runEdenScript is the shared runner for pr-owl's shell-outs:
// exec ~/.eden/bin/<name> <prNumber>, refresh local state on success,
// surface an errMsg on failure. Absolute path via $HOME/.eden/bin
// because Ghostty may not inherit the user's interactive PATH.
func runEdenScript(name string, prNumber int) tea.Cmd {
	return func() tea.Msg {
		home, err := os.UserHomeDir()
		if err != nil {
			return errMsg{fmt.Errorf("resolve $HOME: %w", err)}
		}
		cmd := exec.Command(home+"/.eden/bin/"+name, strconv.Itoa(prNumber))
		if err := cmd.Run(); err != nil {
			return errMsg{fmt.Errorf("%s %d: %w", name, prNumber, err)}
		}
		return fetchLocal()
	}
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

	case tea.KeyMsg:
		return m.handleKey(msg)

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
			cmds = append(cmds, fetchPRs, fetchLocal, fetchMerged)
		}

	case spinner.TickMsg:
		// bubbles/spinner has no Stop method — you stop it by not
		// forwarding its next tick. Gate on !prsReady so it winds
		// down naturally once the initial fetch completes.
		if !m.prsReady {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}

	case prsMsg:
		m.prs = []PR(msg)
		m.prsReady = true
		m.lastFetched = time.Now()
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case mergedMsg:
		m.merged = []PR(msg)
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case localMsg:
		m.localState = map[string]LocalState(msg)
		m.refreshList()
		// LocalState is derived from tmux/git — cheap to refetch, not cached.

	case userMsg:
		m.me = string(msg)
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case errMsg:
		m.err = msg.err
		m.prsReady = true
	}

	return m, tea.Batch(cmds...)
}

// handleKey routes a KeyMsg. When the search input is focused, the
// input consumes most keys — we only intercept esc/enter/tab-out.
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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
		switch {
		case key.Matches(msg, m.keys.Cancel):
			m.search.SetValue("")
			m.search.Blur()
			m.clampCursor()
			m.refreshList()
			return m, nil
		case key.Matches(msg, m.keys.Enter):
			// Blur search (applies filter, keeps value). Overloading
			// Enter is fine — outside search mode Enter opens the
			// selected PR's review; the two paths never collide because
			// this branch runs only while search.Focused().
			m.search.Blur()
			return m, nil
		}
		// Drop non-digit runes upstream — bubbles/textinput's Validate
		// only sets a display error, it doesn't reject input. Non-rune
		// keys (backspace, arrows, delete) pass through so editing works.
		if msg.Type == tea.KeyRunes {
			for _, r := range msg.Runes {
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

	switch {
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Refresh):
		m.prsReady = false
		m.merged = nil
		m.err = nil
		return m, tea.Batch(fetchPRs, fetchLocal, fetchMerged, m.spinner.Tick)
	case key.Matches(msg, m.keys.Up):
		m.moveCursor(-1)
		m.refreshList()
	case key.Matches(msg, m.keys.Down):
		m.moveCursor(1)
		m.refreshList()
	case key.Matches(msg, m.keys.PageUp):
		m.moveCursor(-m.list.Height / 2)
		m.refreshList()
	case key.Matches(msg, m.keys.PageDown):
		m.moveCursor(m.list.Height / 2)
		m.refreshList()
	case key.Matches(msg, m.keys.Home):
		m.cursor = m.firstPRRowIndex()
		m.refreshList()
	case key.Matches(msg, m.keys.End):
		m.cursor = m.lastPRRowIndex()
		m.refreshList()
	case key.Matches(msg, m.keys.Enter):
		if pr := m.selectedPR(); pr != nil {
			// Fire pr-review, then quit pr-owl. tea.Sequence guarantees
			// pr-review has been *started* (spawned + detached) before
			// tea.Quit closes the popup — with tea.Batch the goroutines
			// race and quit can arrive first, tearing down pr-owl before
			// launchDetached even forks the subprocess. launchDetached
			// itself returns near-instantly (Setsid+Start+Release), so
			// Sequence adds no perceptible latency.
			return m, tea.Sequence(launchPRReview(pr.Number), tea.Quit)
		}
	case key.Matches(msg, m.keys.Feedback):
		if pr := m.selectedPR(); pr != nil {
			ls := findLocalForPR(m.localState, pr.Number)
			switch {
			case ls.Session != "":
				// Review window exists — inject the prompt (sync tmux
				// send-keys, ~100ms), then quit.
				return m, tea.Sequence(sendCheckFeedback(ls.Session, pr.Number), tea.Quit)
			case hasPriorConversation(pr.Number):
				// Review window gone (cleaned up) but Claude's conversation
				// state survives on disk. Launch via pr-review with the
				// prompt as arg 2 — pr-review resumes via `claude -c`
				// and submits the prompt in one shot. No timing race.
				return m, tea.Sequence(launchPRReviewWithPrompt(pr.Number, checkFeedbackPrompt), tea.Quit)
			}
			// Truly fresh (no window, no prior conversation) — f is
			// scoped to "check feedback on what you already reviewed".
			// No-op; press Enter first to open an initial review.
		}
	case key.Matches(msg, m.keys.Browser):
		if pr := m.selectedPR(); pr != nil {
			return m, openPRInBrowser(pr.Number)
		}
	case key.Matches(msg, m.keys.Linear):
		if pr := m.selectedPR(); pr != nil {
			return m, openLinearForPR(pr)
		}
	case key.Matches(msg, m.keys.Yank):
		if pr := m.selectedPR(); pr != nil {
			return m, yankPRURL(m.repo, pr.Number)
		}
	case key.Matches(msg, m.keys.Next):
		m.jumpToNextAttention()
		m.refreshList()
	case key.Matches(msg, m.keys.Cleanup):
		if pr := m.selectedPR(); pr != nil {
			// Only fires cleanup if there's a local worktree/session to
			// tear down — otherwise it's a no-op and the errMsg would
			// just noise the UI.
			if ls := findLocalForPR(m.localState, pr.Number); ls.Worktree != "" || ls.Session != "" {
				return m, cleanupPRWorktree(pr.Number)
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
		for next >= 0 && next < len(rows) && rows[next].pr == nil {
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
	// Land on a PR row, not a header.
	rows := m.visibleRows()
	for m.cursor >= 0 && m.cursor < len(rows) && rows[m.cursor].pr == nil {
		m.cursor++
	}
	if m.cursor >= len(rows) {
		m.cursor = m.lastPRRowIndex()
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// selectedPR returns the PR under the cursor, or nil if the cursor
// isn't on a PR row (e.g. list is empty).
func (m model) selectedPR() *PR {
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	return rows[m.cursor].pr
}

// jumpToNextAttention advances the cursor to the next row that wants
// attention — Todo bucket, or any row whose Claude state is amber
// (halted) or green (unread). Wraps around when it hits the end.
// No-op when no attention-needed rows exist.
func (m *model) jumpToNextAttention() {
	rows := m.visibleRows()
	if len(rows) == 0 {
		return
	}
	wants := func(r visibleRow) bool {
		if r.pr == nil {
			return false
		}
		if r.status == StatusTodo && !r.merged {
			return true
		}
		if r.pr != nil {
			ls := findLocalForPR(m.localState, r.pr.Number)
			if ls.ClaudeState == "amber" || ls.ClaudeState == "green" {
				return true
			}
		}
		return false
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
		if rows[i].pr != nil {
			return i
		}
	}
	return 0
}

func (m model) lastPRRowIndex() int {
	rows := m.visibleRows()
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].pr != nil {
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
	m.list.Width = m.width
	m.list.Height = h
	m.help.Width = m.width
	// Leave room for the "search /  " label; clamp so a very narrow
	// terminal doesn't hand textinput a negative width.
	m.search.Width = clampInt(m.width-20, 10, 200)
}

// ------------------------------------------------------------
// Content rendering
// ------------------------------------------------------------

// visibleRows builds the section-grouped row list with the search
// filter applied. Sections with zero visible rows are omitted.
func (m model) visibleRows() []visibleRow {
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
		title := fmt.Sprintf("Merged (last %s)", humanizeDuration(mergedWindow))
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
	h := m.list.Height
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
	if row.pr == nil {
		return row.sectionStyle.Render(row.sectionTitle)
	}
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	local := findLocalForPR(m.localState, row.pr.Number)
	draft := ""
	if row.pr.IsDraft {
		draft = styleDraft.Render(" [draft]")
	}
	age := relativeAge(row.pr.UpdatedAt)
	if row.merged {
		age = "merged " + relativeAge(row.pr.MergedAt)
	}
	return fmt.Sprintf(
		"%s#%-5d %s %s%s (%s) — %s",
		cursor,
		row.pr.Number,
		badges(local, row.pr.IApproved(m.me), row.pr.IReviewed(m.me), row.pr.HasChangesRequested()),
		trim(row.pr.Title, 70),
		draft,
		row.pr.Author.Login,
		age,
	)
}

// ------------------------------------------------------------
// View
// ------------------------------------------------------------

// actionRowView renders the single-line row under the title. It's
// always present (chrome math is simpler that way) and shows one of:
//   - the loading spinner + "loading PRs…" while the initial fetch is pending
//   - the search input when the user is typing
//   - the filter chip when a filter is applied but the input is blurred
//   - a subtle idle hint otherwise
//
// Never more than one visible line — length may exceed width and be
// truncated by the terminal; that's acceptable for a status row.
func (m model) actionRowView() string {
	switch {
	case !m.prsReady && m.err == nil:
		return m.spinner.View() + " " + styleDim.Render("loading PRs…")
	case m.search.Focused():
		return styleSearchLabel.Render("search /") + m.search.View()
	case m.search.Value() != "":
		return styleSearchLabel.Render("filter /"+m.search.Value()) +
			styleDim.Render("   [/] edit   [esc] clear")
	default:
		return m.countsSummary()
	}
}

// countsSummary is the idle-state action row content: a compact
// count-per-group line so a glance tells you today's shape.
func (m model) countsSummary() string {
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
		humanizeDuration(mergedWindow),
	))
}

// titleLine renders the header row: `pr-owl · <repo>` left-aligned,
// `updated Xm ago` right-aligned, padded to fill m.width. Timestamp
// is omitted before the first fetch completes (lastFetched is zero).
func (m model) titleLine(repo string) string {
	left := styleHeader.Render(fmt.Sprintf("pr-owl · %s", repo))
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
	title := styleHeader.Render("pr-owl · help")

	legend := lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   worktree present for pr-<N>[-…] branch", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude halted — blocked on permission/elicitation prompt", styleClaudeAmber.Render("©")),
		fmt.Sprintf("  %s   Claude working — actively processing a turn", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s  Claude finished — unread (result to view; ack with `prefix + a`)", styleClaudeGreen.Render("©")+styleClaudeGreen.Render("*")),
		fmt.Sprintf("  %s   Claude finished — read (acked, session still available)", styleClaudeGreen.Render("©")),
		fmt.Sprintf("  %s   Claude session — no state set (fresh window)", styleClaudeNeutral.Render("©")),
		fmt.Sprintf("  %s   I approved this PR (current verdict)", styleApproved.Render("✓")),
		fmt.Sprintf("  %s   I engaged — commented or requested changes, no approval", styleDim.Render("·")),
		fmt.Sprintf("  %s   changes requested by any reviewer (PR blocked)", styleChangesReqd.Render("⚠")),
	)

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

func (m model) View() string {
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

	if m.err != nil {
		b.WriteString(fmt.Sprintf("\nerror: %v\n", m.err))
		b.WriteString("\n" + m.help.View(m.keys))
		return b.String()
	}

	if !m.prsReady {
		// Loading state is already shown in the action row (spinner + text).
		// Leave the body blank so the eye stays where the movement is.
		return b.String()
	}

	rows := m.visibleRows()
	if len(rows) == 0 {
		if m.search.Value() != "" {
			b.WriteString(fmt.Sprintf("\nno PRs match /%s\n", m.search.Value()))
		} else {
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
//	Slot 1  ⎇   worktree present
//	Slot 2  ©*  Claude session — the trailing `*` (or space) is an unread marker:
//	              ©*  green  = finished, unread (result to view)
//	              ©   green  = finished, read (acked via `prefix + a`)
//	              ©   yellow = working (actively processing)
//	              ©   amber  = halted (blocked on permission/elicitation)
//	              ©   gray   = session exists, no state set (fresh window)
//	Slot 3  ✓   I approved (green) — my current verdict is APPROVED
//	            ·  engaged (dim) — I commented/CR'd but did not approve
//	Slot 4  ⚠   any reviewer currently requesting changes (PR blocked)
//
// Absent = single space so column alignment stays. Slot 2 is always
// 2 cells wide (glyph + `*`|space) because of the unread marker.
func badges(ls LocalState, iApproved, iEngaged, hasCR bool) string {
	var parts []string

	if ls.Worktree != "" {
		parts = append(parts, styleWorktree.Render("⎇"))
	} else {
		parts = append(parts, " ")
	}

	// Claude slot: © + unread marker (`*` for unread green, else space).
	switch ls.ClaudeState {
	case "amber":
		parts = append(parts, styleClaudeAmber.Render("©")+" ")
	case "working":
		parts = append(parts, styleClaudeWorking.Render("©")+" ")
	case "green":
		parts = append(parts, styleClaudeGreen.Render("©")+styleClaudeGreen.Render("*"))
	case "read":
		parts = append(parts, styleClaudeGreen.Render("©")+" ")
	default:
		if ls.Session != "" {
			parts = append(parts, styleClaudeNeutral.Render("©")+" ")
		} else {
			parts = append(parts, "  ")
		}
	}

	switch {
	case iApproved:
		parts = append(parts, styleApproved.Render("✓"))
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

// trim shortens s to max n characters, appending an ellipsis if it was cut.
func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

// relativeAge formats an RFC3339 timestamp as "5m", "3h", "2d", or "3w".
func relativeAge(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
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

func humanizeDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
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

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "pr-owl: %v\n", err)
		os.Exit(1)
	}
}
