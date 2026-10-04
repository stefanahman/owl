package main

import (
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

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

// ansiRe matches the escape codes lipgloss wraps styled text in.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// stripANSI removes the styling from a rendered line. The inactive
// pane is drawn dim, and dimming text that already carries colours
// means taking the colours off first.
func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// dimBlock renders a whole pane as inactive: every line stripped of
// its own styling and dimmed, so which pane has the keys is never in
// question.
func dimBlock(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = styleDim.Render(stripANSI(line))
	}
	return strings.Join(lines, "\n")
}

// stateUnlessSection is the state name a row shows: none when the
// section it sits in already says it. "Backlog" inside Backlog is
// noise; "In Review" inside In progress is the point.
func stateUnlessSection(name, section string) string {
	if strings.EqualFold(name, section) {
		return ""
	}
	return name
}

// localOf is the row's overlay: the worktree, window and agent state
// of its workspace.
func (m model) localOf(r visibleRow) LocalState {
	if r.pr != nil {
		// One of your own opens into its branch's workspace, whose name
		// carries the issue key and not the PR number.
		if r.mine {
			if ls := findLocalBranch(m.localState, r.pr.HeadRefName); ls.Worktree != "" {
				return ls
			}
		}
		return findLocalForPR(m.localState, r.pr.Number)
	}
	if r.issue != nil {
		return findLocalBy(m.localState, func(name string) bool { return matchesIssue(name, r.issue.Key) })
	}
	if r.project != nil {
		st, taken := m.projectStates[r.project.ID], takenNames(m.projectStates, r.project.ID)
		return findLocalBy(m.localState, func(name string) bool { return matchesProject(name, *r.project, st, taken) })
	}
	return LocalState{}
}

// started reports whether a row's work has been begun — a worktree or
// a window for it, or a conversation Claude kept on disk after the
// workspace was closed. It is what `when: conversation` gates on.
//
// It asks localOf rather than building a name from the row's number,
// which is what `f` on one of your own PRs used to do: that workspace
// is named for the branch, so a lookup by `pr-<N>` found nothing and
// the key did nothing at all.
func (m model) started(r visibleRow) bool {
	if r.header() {
		return false
	}
	if ls := m.localOf(r); ls.Window != "" || ls.Worktree != "" {
		return true
	}
	if m.repoDir == "" {
		return false
	}
	// Nothing local: Claude may still hold the conversation from a
	// workspace that has been closed since, under the directory that
	// workspace had.
	switch {
	case r.issue != nil:
		return hasConversationFor(filepath.Join(m.repoDir, m.cfg.WorktreesDir, r.issue.Branch))
	case r.pr != nil && r.mine:
		// One of yours has two possible names and the TUI cannot ask
		// GitHub which: the branch's, when it carries an issue key, and
		// `pr-<N>-<slug>` when it does not. Both are cheap to look for.
		if r.pr.HeadRefName != "" &&
			hasConversationFor(filepath.Join(m.repoDir, m.cfg.WorktreesDir, path.Base(r.pr.HeadRefName))) {
			return true
		}
		return hasPriorConversation(m.repoDir, m.cfg.WorktreesDir, r.pr.Number)
	case r.pr != nil:
		return hasPriorConversation(m.repoDir, m.cfg.WorktreesDir, r.pr.Number)
	}
	return false
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
		// Todo is what `n` is for — except where the request is the
		// team's and not yours. By this team's convention that is an FYI,
		// and a key that means "next thing that needs me" walking you
		// into one is the key doing the opposite of its job. The row
		// stays in the list and stays reachable with j/k; only the
		// attention key stops treating it as attention.
		if r.pr != nil && r.status == StatusTodo && !r.merged && !r.pr.teamAskedNotYou(m.me) {
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
	// contentHeight is what the rows have between the chrome. The
	// viewport's own height is the focused pane's share of it, which
	// refreshList sets — deriving it from the viewport instead would
	// shrink it again on every resize.
	m.contentHeight = h
	m.list.SetWidth(m.width)
	m.list.SetHeight(h)
	m.help.SetWidth(m.width)
	// Leave room for the "search /  " label; clamp so a very narrow
	// terminal doesn't hand textinput a negative width.
	m.search.SetWidth(clampInt(m.width-20, 10, 200))
}
