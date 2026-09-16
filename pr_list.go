// The pull request list's own side: the rows of the review queue, the
// two panes it is read in, and the badges a row carries.
//
// The other lists have had a file each — issue_list.go, project_list.go,
// mine.go — while this one stayed in main.go with the model. What is
// shared still lives there: renderRow, countsSummary and legend all
// dispatch by which list is open, and only their pull request tails
// are here.
//
// Two panes are this list alone. A review queue and your own pull
// requests are different questions, and the answer to one is no help
// with the other, so they are shown together and Tab moves between
// them.
package main

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// elsewhere is the repository a row belongs to when that is not the
// one owl is standing in, and "" when it is.
//
// The list spans every repo pr.owners covers, and a pull request number
// means nothing without its repository: #3 is a different pull request
// in each of them. `open` fetches `pull/<N>/head` from the repo owl is
// in, so opening a row from elsewhere would quietly check out the
// wrong work — or fail, on a repo where that number is not taken yet.
// Until a row can be opened in its own repo, saying so is the honest
// answer.
func (m model) elsewhere(row visibleRow) string {
	if row.pr == nil || row.pr.Repo == "" || m.repo == "" {
		return ""
	}
	if strings.EqualFold(row.pr.Repo, m.repo) {
		return ""
	}
	return row.pr.Repo
}

// visibleReviewRows is the PR list's review pane: what needs your
// review, grouped by where you sit on it.
func (m model) visibleReviewRows() []visibleRow {
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
		title := "Merged (last " + m.cfg.MergedWindow.Text + ")"
		out = append(out, visibleRow{sectionTitle: title, sectionStyle: styleSectionMerged, merged: true})
		for i := range mergedVisible {
			out = append(out, visibleRow{pr: &mergedVisible[i], merged: true})
		}
	}

	return out
}

// focusWhereTheRowsAre moves the focus to the pane that has something
// in it, while the reader has not said otherwise.
//
// The PR list opens on the review queue, and the counts and the cursor
// follow the focused pane. A repo where nothing is waiting on you —
// a solo project, where every pull request is your own — opened on an
// empty pane, counted it, and reported nothing while your own work sat
// in the pane below.
func (m *model) focusWhereTheRowsAre() {
	if !m.panes() || m.paneChosen {
		return
	}
	if len(m.visibleRows()) == 0 && len(m.otherPaneRows()) > 0 {
		m.mineFocus = !m.mineFocus
		m.cursor, m.otherCursor = m.otherCursor, m.cursor
		m.keys = newKeyMap(m.cfg.Keys, m.bindings())
		m.labelKeysForPane()
		m.clampCursor()
	}
}

// labelKeysForPane names the keys after what they do in the pane that
// has them: the help line is the only place a key explains itself, and
// "open review" is wrong on a pull request of your own.
func (m *model) labelKeysForPane() {
	enter, cleanup := "open review", "clean up worktree"
	if m.mineFocus {
		enter, cleanup = "open your PR", "clean up worktree"
	}
	m.keys.Enter.SetHelp(m.keys.Enter.Help().Key, enter)
	m.keys.Cleanup.SetHelp(m.keys.Cleanup.Help().Key, cleanup)
}

// otherPaneRows is the pane that does not have the cursor.
func (m model) otherPaneRows() []visibleRow {
	if m.mineFocus {
		return m.visibleReviewRows()
	}
	return m.visibleMineRows()
}

// prScopeLabel is what the PR list's title names. One repository when
// that is all it holds, and the owners when it spans them: a title
// reading `owl · stefanahman/owl` over rows from three repositories
// names one of them and misses the point of the list.
func (m model) prScopeLabel(repo string) string {
	if m.here || !m.cfg.PR.Spans() {
		return repo
	}
	return scopeLabel(m.cfg.PR.Owners)
}

// focusedPaneHeight is how many rows the focused pane gets. The same
// number sizes the viewport and slices the rows into it, so the cursor
// cannot scroll out of a window narrower than the one it was measured
// against.
func (m model) focusedPaneHeight() int {
	avail := m.contentHeight - 2 // one heading each
	if avail < 2 {
		avail = 2
	}
	h, _ := splitPaneHeights(avail, len(m.visibleRows()), len(m.otherPaneRows()))
	return h
}

// panesView draws the two panes of the PR list.
//
// Their places are fixed — the review queue above, your own below —
// and Tab moves only the highlight. Swapping them would put the rows
// you were reading somewhere else every time you changed pane, which
// is the eye movement the two panes exist to save.
//
// Only the focused pane scrolls: it has the viewport and the cursor.
// The other shows what fits, dimmed, with a last line saying what it
// cut; Tab is how you reach the rest, which is also how you get its
// keys.
func (m model) panesView() string {
	other := m.otherPaneRows()
	avail := m.contentHeight - 2 // one heading each
	if avail < 2 {
		avail = 2
	}
	_, otherH := splitPaneHeights(avail, len(m.visibleRows()), len(other))

	// The focused pane is the viewport, already sized and filled by
	// refreshList; the other is drawn here and dimmed.
	focused := m.list.View()
	inactive := m.inactivePane(other, otherH)
	review := fmt.Sprintf("To review (%d)", len(m.prs))
	mine := fmt.Sprintf("Mine (%d)", len(m.mine))

	if m.mineFocus {
		return dimBlock(review) + "\n" + inactive + "\n" +
			styleHeader.Render(mine) + "\n" + focused
	}
	return styleHeader.Render(review) + "\n" + focused + "\n" +
		dimBlock(mine) + "\n" + inactive
}

// inactivePane renders the rows of the pane without the cursor: as
// many as it has room for, dimmed, and a last line naming what did not
// fit rather than cutting silently.
func (m model) inactivePane(rows []visibleRow, h int) string {
	if h <= 0 || len(rows) == 0 {
		return ""
	}
	lines := make([]string, 0, h)
	for i := 0; i < h && i < len(rows); i++ {
		lines = append(lines, m.renderRow(rows[i], false))
	}
	if cut := len(rows) - h; cut > 0 {
		lines[len(lines)-1] = fmt.Sprintf("  … %d more, %s to go there", cut+1, m.keys.Pane.Help().Key)
	}
	return dimBlock(strings.Join(lines, "\n"))
}

// splitPaneHeights gives each pane the rows it wants where they fit,
// and splits the shortfall so neither is squeezed to nothing by the
// other having plenty.
func splitPaneHeights(avail, want, otherWant int) (int, int) {
	if want+otherWant <= avail {
		return want, otherWant
	}
	half := avail / 2
	switch {
	case want <= half:
		return want, avail - want
	case otherWant <= avail-half:
		return avail - otherWant, otherWant
	}
	return half, avail - half
}

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
