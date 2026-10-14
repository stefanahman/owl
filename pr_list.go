// The pull request list's own side: the rows of the review queue, the
// two panes it is read in, and the badges a row carries.
//
// The other lists have a file each — issue_list.go, project_list.go,
// mine.go — and so does this one. What all of them share is in
// view.go: renderRow, countsSummary and legend dispatch by which list
// is open, and only their pull request tails are here.
//
// Two panes are this list alone. A review queue and your own pull
// requests are different questions, and the answer to one is no help
// with the other, so they are shown together and Tab moves between
// them.
package main

import (
	"fmt"
	"slices"
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
	// Both fetches must have answered. They land in either order, and
	// a pane that has not been fetched yet looks exactly like an empty
	// one — deciding on that moved the focus to your own work whenever
	// the review queue happened to be slower, and never moved it back.
	if !m.prsAnswered || !m.mineAnswered {
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
	m.keys.Drill.SetHelp(m.keys.Drill.Help().Key, "the PR's stack")
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

// repoWidth is the repository column: enough for the names a personal
// desk holds (owl, thevake, fortoj, norrbrunn) and trimmed past that.
const repoWidth = 12

// repoCell is the column saying which repository a row is from, and ""
// where the list holds only one — then every row carries the same
// answer, which is no answer, and the title bar has already given it.
//
// The name without its owner: the owners are in the title, and it is
// the name that tells owl from thevake. Two owners with a repository
// named alike would read the same here, which costs a glance at a
// title bar that is already on screen.
func (m model) repoCell(pr *PR) string {
	if !m.cfg.PR.Spans() || m.here || pr == nil || pr.Repo == "" {
		return ""
	}
	name := pr.Repo
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return cell(name, repoWidth, styleDim) + " "
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

// stackLegend explains the stack column, in both panes' legends.
var stackLegend = lipgloss.JoinVertical(lipgloss.Left,
	fmt.Sprintf("  %s  the stack whose bottom is #1201, layer 3 of 6; %s is below a fork",
		styleStack.Render("≡1201 3/6"), styleStack.Render("≡1201 3")),
	fmt.Sprintf("  %s  after it: a stack review holds the layer — → then ↵ on its line goes there",
		styleWorktree.Render("⎇")+" "+styleClaudeWorking.Render("©")),
)

// graphOf is the stack graph of the repository a pull request is in,
// or nil before the list has one.
func (m model) graphOf(pr *PR) *prGraph {
	repo := pr.Repo
	if repo == "" {
		repo = m.repo
	}
	return m.stacks[repo]
}

// stackLabel is where a stacked row sits: the stack, named by its
// bottom PR so the rows of one stack read alike down the list, and the
// layer — ≡1201 3/6, the third of a line six long, or ≡1201 3 where the
// stack forks above it and the length is not one number. "" for a row
// that is no layer.
func (m model) stackLabel(pr *PR) string {
	g := m.graphOf(pr)
	// Merged is no layer, whatever a graph fetched before the merge
	// still holds: the merged rows land in a fetch of their own.
	if g == nil || pr.MergedAt != "" {
		return ""
	}
	l, err := g.line(pr.Number)
	if err != nil || !l.stacked() {
		return ""
	}
	layer := slices.Index(l.numbers(), pr.Number) + 1
	if len(l.Tips) > 0 {
		return fmt.Sprintf("≡%d %d", l.PRs[0].Number, layer)
	}
	return fmt.Sprintf("≡%d %d/%d", l.PRs[0].Number, layer, len(l.PRs))
}

// stackColumn is the stack column's shape over the open rows of both
// panes, so it lines up from one pane to the other: the widest label —
// 0, and no column at all, while no row is a layer — and whether any
// row has a stack review open on it, which is what the column's badges
// need room for. Merged rows are never layers: the graph holds the open
// pull requests.
func (m model) stackColumn() (width int, badges bool) {
	for _, list := range [][]PR{m.prs, m.mine} {
		for i := range list {
			width = max(width, lipgloss.Width(m.stackLabel(&list[i])))
			if _, ok := m.stackReview(&list[i]); ok {
				badges = true
			}
		}
	}
	return width, badges
}

// stackCell is the row's stack column: its label in the stack colour,
// padded to the column, then the stack review's badges when one holds
// the row — blank on a row that is no layer.
func (m model) stackCell(pr *PR) string {
	if m.stackCol == 0 {
		return ""
	}
	label := m.stackLabel(pr)
	cell := strings.Repeat(" ", max(m.stackCol-lipgloss.Width(label), 0))
	if label != "" {
		cell = styleStack.Render(label) + cell
	}
	if m.stackBadges {
		ls, ok := m.stackReview(pr)
		if !ok {
			ls = LocalState{}
		}
		cell += " " + workspaceBadges(ls, "")
	}
	return cell + "  "
}

// stackReview is the stack review that holds a row: the workspace of a
// line whose record held the pull request at its last open, while its
// worktree or its window is there. A layer below a fork can be on
// several lines under review, and the one whose Claude most wants you
// speaks for them — the row's own ⎇ © slots stay its own review's,
// which is what ↵ opens.
func (m model) stackReview(pr *PR) (LocalState, bool) {
	repo := pr.Repo
	if repo == "" {
		repo = m.repo
	}
	var best LocalState
	found := false
	for _, r := range m.stackRecords {
		if !strings.EqualFold(r.Repo, repo) || !slices.Contains(r.PRs, pr.Number) {
			continue
		}
		ls, ok := m.localState[r.name]
		if !ok || ls.Worktree == "" && ls.Window == "" {
			continue // a closed stack keeps its record, and holds nothing
		}
		if !found || stateClaim(ls.ClaudeState) > stateClaim(best.ClaudeState) {
			best, found = ls, true
		}
	}
	return best, found
}

// stateClaim orders agent states by how much they want you: blocked on you,
// then done and unread, then working, then idle.
func stateClaim(state string) int {
	switch state {
	case agentBlocked:
		return 4
	case agentDone:
		return 3
	case agentWorking:
		return 2
	case agentIdle:
		return 1
	}
	return 0
}

// PR titles take what the window leaves them, between a floor that
// still reads as a title and a ceiling past which a row stops being
// one line of reading. The suffix is reserved for what follows the
// title: the author, or a pull request's issue chips.
const (
	prTitleFloor   = 24
	prTitleCeiling = 100
	prSuffix       = 20
)

// prTitleWidth is the title column after a prefix this wide; 0 before
// the window has a size, and the caller keeps its old fixed width.
func (m model) prTitleWidth(prefix int) int {
	if m.width == 0 {
		return 0
	}
	return clampInt(m.width-prefix-prSuffix, prTitleFloor, prTitleCeiling)
}

// titleCell is a title and its draft mark cut together to w and padded
// to it, so what follows lines up down the list.
func titleCell(title, draft string, w int) string {
	s := trim(title, w-lipgloss.Width(draft)) + draft
	return s + strings.Repeat(" ", max(w-lipgloss.Width(s), 0))
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
// verdict is what this list says about your own review of a pull
// request: the glyph is what you did, the colour whether it still
// covers the head.
//
// Named fields rather than the four positional booleans this replaces.
// One of them is the reason: `stale` was handed `row.status ==
// StatusWaitingForYou` at the call site, which is not the same
// question as "is my review stale" and must not become it. A merged
// row carries no status at all, so that expression is false for every
// one of them — and a finished pull request going amber for a push
// nobody can re-review would be wrong.
type verdict struct{ approved, engaged, stale, changesRequested bool }

// verdictOf reads the row's verdict. The status comes from the row
// because the sections already computed it; everything else the pull
// request answers itself.
func verdictOf(row visibleRow, me string) verdict {
	return verdict{
		approved:         row.pr.IApproved(me),
		engaged:          row.pr.IReviewed(me),
		stale:            row.status == StatusWaitingForYou,
		changesRequested: row.pr.HasChangesRequested(),
	}
}

// render is slots 3 and 4: my verdict, and whether anyone is blocking.
func (v verdict) render() string {
	var parts []string
	switch {
	case v.approved && v.stale:
		parts = append(parts, styleReviewStale.Render("✓"))
	case v.approved:
		parts = append(parts, styleApproved.Render("✓"))
	case v.engaged && v.stale:
		parts = append(parts, styleReviewStale.Render("·"))
	case v.engaged:
		parts = append(parts, styleDim.Render("·"))
	default:
		parts = append(parts, " ")
	}
	if v.changesRequested {
		parts = append(parts, styleChangesReqd.Render("⚠"))
	} else {
		parts = append(parts, " ")
	}
	return strings.Join(parts, " ")
}

// badges is the review row's four slots: the two every list shows for
// a workspace, then the two that are this list's own.
//
// The first two were a verbatim copy of workspaceBadges — thirty lines
// of which twenty had to be changed in two places or drift apart.
func badges(ls LocalState, starting string, v verdict) string {
	return workspaceBadges(ls, starting) + " " + v.render()
}
