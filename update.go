package main

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

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
		if msg.me != "" {
			m.me = msg.me
		}
		m.ready, m.prsAnswered = true, true
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

	case cancelledMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.cancelledIssues = msg.issues
		m.clampCursor()
		m.refreshList()
		m.persistCache()

	case drillIssuesMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.drillIssues = msg.issues
		m.ready = true
		m.refreshing = false
		m.err = nil
		m.lastFetched = time.Now()
		m.clampCursor()
		m.refreshList()

	case doneProjectsMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.doneProjects = msg.projects
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

	case mineMsg:
		if msg.gen != m.fetchGen {
			break
		}
		m.mine = msg.prs
		m.mineMerged = msg.merged
		m.ready, m.mineAnswered = true, true
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
		m.projectStates = loadProjectStates()
		m.refreshList()
		// LocalState is derived from tmux/git — cheap to refetch, not cached.

	case localTickMsg:
		cmds = append(cmds, m.fetchLocal, localTick(m.stateSignal != nil))

	case statesMsg:
		// Only the states: the worktrees stay as the last full read left
		// them, and the merge happens here because this is where the
		// overlay is current.
		m.localState = withStates(m.localState, msg)
		m.refreshList()

	case watchMsg:
		m.stateDriver, m.stateSignal = msg.driver, msg.signal
		if msg.signal == nil {
			break // no watch: the timer is the mechanism, as it was
		}
		// The driver arriving is itself news: it is the one a watch is
		// live on, so this first read is the cheap kind.
		cmds = append(cmds, m.fetchStates, waitForState(m.stateSignal))

	case stateChangedMsg:
		if !msg.open {
			// The watch is gone. The timer is still running, so the list
			// keeps refreshing, just no longer the instant it changes.
			m.stateSignal = nil
			break
		}
		cmds = append(cmds, m.fetchStates, waitForState(m.stateSignal))

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
	case key.Matches(msg, m.keys.Pane):
		if m.panes() {
			// The cursors swap with the focus, so each pane comes back to
			// the row you left it on.
			m.mineFocus = !m.mineFocus
			m.paneChosen = true // you have said which pane you want
			m.cursor, m.otherCursor = m.otherCursor, m.cursor
			m.keys = newKeyMap(m.cfg.Keys, m.bindings())
			m.labelKeysForPane()
			m.clampCursor()
			m.refreshList()
		}
	case key.Matches(msg, m.keys.Drill):
		// Only from a project row, and only into a project: the drill is
		// one level, project to its issues.
		if p := m.selectedProject(); p != nil {
			m.drill, m.drillCursor, m.drillIssues = p, m.cursor, nil
			m.sc = features // the rows are issues now, and so is their overlay
			m.cursor, m.err = 0, nil
			m.search.SetValue("")
			m.refreshList()
			return m, tea.Batch(m.fetchDrillIssues, m.fetchIssuePRs, m.fetchLocal)
		}
	case key.Matches(msg, m.keys.Back):
		if m.drill != nil {
			m.drill, m.drillIssues = nil, nil
			m.sc = projects
			m.cursor, m.err = m.drillCursor, nil
			m.search.SetValue("")
			m.refreshList()
			return m, m.fetchLocal
		}
	case key.Matches(msg, m.keys.Enter), key.Matches(msg, m.keys.Start):
		if row, ok := m.selectedRow(); ok {
			if other := m.elsewhere(row); other != "" {
				m.notice = fmt.Errorf("#%d is in %s and owl is in %s — open it from there", row.pr.Number, other, m.repo)
				return m, nil
			}
			if key.Matches(msg, m.keys.Enter) {
				return m.launch(row.id(), "opening "+row.label()+"…", m.openWorkspace(row.noun(), row.id(), ""), true)
			}
			return m.launch(row.id(), "starting "+row.label()+"…", m.startWorkspace(row.noun(), row.id(), ""), false)
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
				if other := m.elsewhere(row); other != "" {
					m.notice = fmt.Errorf("#%d is in %s and owl is in %s — close it from there", row.pr.Number, other, m.repo)
					return m, nil
				}
				return m.launch(row.id(), "closing "+row.label()+"…", m.closeWorkspace(row.noun(), row.id()), false)
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
