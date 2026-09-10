// The project list's own side of the model: its rows, their
// rendering, its fetch and its cache. The chrome, the cursor, the
// search and the children are the model's, shared with the PR and
// issue lists.
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fetchDoneProjects asks the tracker for what the user finished inside
// doneProjectWindow. A failure leaves the Done section empty rather
// than failing the list: the open projects are the point.
func (m model) fetchDoneProjects() tea.Msg {
	gen := m.fetchGen
	if m.tracker == nil {
		return doneProjectsMsg{gen, nil}
	}
	projects, err := m.tracker.DoneProjects(time.Now().Add(-doneProjectWindow))
	if err != nil {
		return doneProjectsMsg{gen, nil}
	}
	return doneProjectsMsg{gen, projects}
}

// fetchProjects asks the tracker for the open projects the user works
// in. The issue list's fetch runs beside it: the number that says
// where you stand is yours over the project's own count, and the
// first half of that comes from the issues.
func (m model) fetchProjects() tea.Msg {
	gen := m.fetchGen
	if m.tracker == nil {
		return errMsg{gen, fmt.Errorf("no issue tracker configured (linear.token)")}
	}
	projects, err := m.tracker.Projects()
	if err != nil {
		return errMsg{gen, err}
	}
	return projectsMsg{gen, projects}
}

// doneProjectWindow is how far back the Done section reaches. A week,
// not the issue list's day: projects finish on a different clock, and
// one closed on Monday is still news on Friday.
const (
	doneProjectWindow      = 7 * 24 * time.Hour
	doneProjectWindowLabel = "7d"
)

// projectSections are the project list's groups, in order: what is
// moving, what is next, what waits, what just finished. Linear's
// `paused` sits with the backlog — it is not being worked on either
// way.
var projectSections = []struct {
	title string
	note  string // dim, after the title
	style lipgloss.Style
	types []string
}{
	{"In progress", "", styleSectionOK, []string{"started"}},
	{"Planned", "", styleSectionTodo, []string{"planned"}},
	{"Backlog", "", styleSectionWait, []string{"backlog", "paused"}},
	// "Completed", not the issue list's "Done": each section is named
	// what the tracker calls that state, which is also what stops the
	// row repeating it.
	{"Completed", doneProjectWindowLabel, styleSectionMerged, []string{"completed"}},
}

// visibleProjectRows groups the projects by status type, newest change
// first within a group, with the search filter — a name fragment,
// case-insensitively — applied.
func (m model) visibleProjectRows() []visibleRow {
	filter := strings.ToLower(m.search.Value())
	// The open list and the done one are fetched apart; the sections
	// split them again by status type, and Projects() excludes what
	// DoneProjects() returns, so nothing lands twice.
	all := make([]Project, 0, len(m.projects)+len(m.doneProjects))
	all = append(append(all, m.projects...), m.doneProjects...)
	var out []visibleRow
	for _, sec := range projectSections {
		var members []Project
		for _, p := range all {
			if !contains(sec.types, p.State.Type) {
				continue
			}
			if filter != "" && !strings.Contains(strings.ToLower(p.Name), filter) {
				continue
			}
			// The window is the query's, but a cache read from an earlier
			// week would smuggle older ones in: hold the line here too.
			if p.State.Type == "completed" && time.Since(p.CompletedAt) > doneProjectWindow {
				continue
			}
			members = append(members, p)
		}
		if len(members) == 0 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].UpdatedAt.After(members[j].UpdatedAt) })
		out = append(out, visibleRow{sectionTitle: sec.title, sectionNote: sec.note, sectionStyle: sec.style})
		for i := range members {
			out = append(out, visibleRow{project: &members[i], sectionTitle: sec.title})
		}
	}
	return out
}

// mineIn counts the user's open issues in the project, by project id
// rather than name — two projects may be named alike, and an id from
// the same Linear that named the project cannot drift.
func (m model) mineIn(p Project) int {
	n := 0
	for _, is := range m.issues {
		if is.Project.ID != "" && is.Project.ID == p.ID {
			n++
		}
	}
	return n
}

// The project row's columns. As on the issue row, everything but the
// name is fixed width and the name takes what the window leaves.
const (
	barCells        = 10
	mineWidth       = 9
	milestoneWidth  = 6
	nameFloor       = 24
	nameCeiling     = 60
	priorityWidth   = 3  // !!!
	dueWidth        = 8  // "10d late"
	initiativeWidth = 16 // "Capture + Refine"
	leadWidth       = 9  // a given name, as the issue row's assignee
	// projectFixed is everything on the row that is always there:
	// cursor, the bar, the percentage, the counts, the milestones, the
	// state, gaps.
	projectFixed = 2 + 5 + 2 + barCells + 1 + 4 + 2 + mineWidth + 2 + milestoneWidth + 2 + stateWidth
)

// projectCols is how much each of the optional columns gets, 0 for one
// that does not fit.
//
// All four together are wider than the row a popup gets, so rather
// than wrapping — which would cost a line per project and undo the
// overview — the row sheds them, least telling first: the lead, whose
// name is on most rows the same one; then the initiative; then the
// date; and last the priority, which is three cells and the thing you
// asked the list for.
type projectCols struct{ priority, due, initiative, lead int }

// width is what the optional columns cost, each with the gap before
// it: two for the three that trail the row, one for the priority,
// which sits tight against the name like the issue row's does.
func (c projectCols) width() int {
	n := 0
	if c.priority > 0 {
		n += c.priority + 1
	}
	for _, w := range []int{c.due, c.initiative, c.lead} {
		if w > 0 {
			n += w + 2
		}
	}
	return n
}

func (m model) projectCols() projectCols {
	c := projectCols{priorityWidth, dueWidth, initiativeWidth, leadWidth}
	if m.width == 0 {
		return c
	}
	for _, drop := range []*int{&c.lead, &c.initiative, &c.due, &c.priority} {
		if projectFixed+c.width()+nameFloor <= m.width {
			break
		}
		*drop = 0
	}
	return c
}

func (m model) projectNameWidth() int {
	if m.width == 0 {
		return 44
	}
	return clampInt(m.width-projectFixed-m.projectCols().width(), nameFloor, nameCeiling)
}

// finished reports whether a project has left the board, whichever way
// it left.
func finished(stateType string) bool {
	return stateType == "completed" || stateType == "canceled"
}

// relativeDue reads Linear's TimelessDate against today: `in 12d`,
// `today`, `10d late`. "" when the project has no target date, which
// is most of them.
//
// Whole days, both sides: a target date carries no time, so measuring
// from this instant would call something due this evening "in 0d" and
// something due at dawn "late".
func relativeDue(date string) string {
	if date == "" {
		return ""
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return ""
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, t.Location())
	days := int(t.Sub(today).Hours() / 24)
	switch {
	case days == 0:
		return "today"
	case days < 0:
		return fmt.Sprintf("%dd late", -days)
	case days < 14:
		return fmt.Sprintf("in %dd", days)
	case days < 70:
		return fmt.Sprintf("in %dw", days/7)
	}
	return fmt.Sprintf("in %dmo", days/30)
}

// progressBar draws Linear's own fraction as ten cells. A parked
// project's bar is dim throughout: the progress is still true, it is
// just not moving.
func progressBar(f float64, dim bool) string {
	full := int(f*barCells + 0.5)
	if full > barCells {
		full = barCells
	}
	if full < 0 {
		full = 0
	}
	filled := styleApproved
	if dim {
		filled = styleDim
	}
	return filled.Render(strings.Repeat("▓", full)) + styleDim.Render(strings.Repeat("░", barCells-full))
}

// renderProjectRow: cursor, name, the progress bar, your open issues
// over every issue the project holds, its milestone count, and the
// state name only where it says something the section does not.
func (m model) renderProjectRow(row visibleRow, selected bool) string {
	p := row.project
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	starting := ""
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	state := stateUnlessSection(p.State.Name, row.sectionTitle)
	milestones := ""
	if n := len(p.Milestones.Nodes); n > 0 {
		milestones = fmt.Sprintf("%d ms", n)
	}
	// Parked: present, and not moving. The whole row recedes rather
	// than leaving its section, because a paused project is still one
	// of yours and still where Linear puts it.
	dim := m.cfg.Project.dimmed(p.State.Name)
	// Your share is the point of the row, so it stays undimmed when
	// there is one and recedes when the project holds nothing of yours.
	mine := m.mineIn(*p)
	counts, style := fmt.Sprintf("%d/%d", mine, p.Scope), styleDim
	if mine > 0 && !dim {
		style = lipgloss.NewStyle()
	}
	w := m.projectNameWidth()
	name := fmt.Sprintf("%-*s", w, trim(p.Name, w))
	if dim {
		name = styleDim.Render(name)
	}
	// A date already past is the one thing on this row that is not just
	// information, so it is the one thing that is not dim — but only
	// while the project can still act on it. A finished project's target
	// date is history, and a completed row reading "10d late" in red
	// asks for something that cannot be done.
	cols := m.projectCols()
	due, dueStyle := "", styleDim
	if !finished(p.State.Type) {
		due = relativeDue(p.TargetDate)
		if strings.HasSuffix(due, "late") && !dim {
			dueStyle = styleChangesReqd
		}
	}
	prio := ""
	if cols.priority > 0 {
		prio = cell(priorityMark(p.Priority), cols.priority, styleDim) + " "
	}
	out := fmt.Sprintf(
		"%s%s %s%s  %s %3.0f%%  %s  %s",
		cursor,
		workspaceBadges(m.localOf(row), starting),
		prio,
		name,
		progressBar(p.Progress, dim),
		p.Progress*100,
		cell(counts, mineWidth, style),
		cell(milestones, milestoneWidth, styleDim),
	)
	for _, c := range []struct {
		text  string
		width int
		style lipgloss.Style
	}{
		{due, cols.due, dueStyle},
		{p.Initiative(), cols.initiative, styleDim},
		{firstWord(p.Lead.Name), cols.lead, styleDim},
		{state, stateWidth, styleDim},
	} {
		if c.width > 0 {
			// Two spaces, as between every other pair on this row. One is
			// invisible when the text fills its column exactly — which
			// "Capture + Refine" does, at sixteen — and two fields then
			// read as one.
			out += "  " + cell(c.text, c.width, c.style)
		}
	}
	return strings.TrimRight(out, " ")
}

// projectCountsSummary is the idle-state action row of the project list.
func (m model) projectCountsSummary() string {
	counts := map[string]int{}
	mine := 0
	for _, p := range m.projects {
		counts[p.State.Type]++
		mine += m.mineIn(p)
	}
	return styleDim.Render(fmt.Sprintf(
		"%d projects · %d in progress · %d planned · %d backlog · %d issues yours",
		len(m.projects),
		counts["started"],
		counts["planned"],
		counts["backlog"]+counts["paused"],
		mine,
	))
}

// projectLegend explains the project row's columns.
func (m model) projectLegend() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   worktree and window for the project's conversation", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude in it — working, blocked, done, idle as on the other lists", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s  Linear's own progress for the project", progressBar(0.6, false)),
		"  12/127      your open issues in it, over every issue it holds",
		"  11 ms       milestones: the project's own structure, and where its specs live",
		"",
		styleHeader.Render("Which projects"),
		"  the ones you lead, belong to, or have an open issue in — membership alone",
		"  misses the project most of your work is in",
	)
}

// fetchDrillIssues asks the tracker for every open issue of the
// project being drilled into, whoever it belongs to.
func (m model) fetchDrillIssues() tea.Msg {
	gen := m.fetchGen
	if m.tracker == nil || m.drill == nil {
		return drillIssuesMsg{gen, nil}
	}
	issues, err := m.tracker.ProjectIssues(m.drill.ID)
	if err != nil {
		return errMsg{gen, err}
	}
	return drillIssuesMsg{gen, issues}
}

// visibleDrillRows groups a project's issues by milestone, in the
// project's own order, with the search filter applied. The section a
// row sits in is its milestone, so renderIssueRow always prints the
// state: a milestone name never collides with a status.
func (m model) visibleDrillRows() []visibleRow {
	filter := strings.ToLower(m.search.Value())
	var kept []Issue
	for _, is := range m.drillIssues {
		if filter != "" &&
			!strings.Contains(strings.ToLower(is.Key), filter) &&
			!strings.Contains(strings.ToLower(is.Title), filter) &&
			!strings.Contains(strings.ToLower(is.Assignee.Name), filter) {
			continue
		}
		kept = append(kept, is)
	}
	var out []visibleRow
	for _, sec := range milestoneSections(kept) {
		out = append(out, visibleRow{sectionTitle: sec.title, sectionStyle: styleSectionWait})
		for i := range sec.issues {
			out = append(out, visibleRow{issue: &sec.issues[i], sectionTitle: sec.title})
		}
	}
	return out
}

// drillCountsSummary is the action row while drilled: the project's
// own numbers, and how much of it is yours.
func (m model) drillCountsSummary() string {
	mine := 0
	for _, is := range m.drillIssues {
		if is.Assignee.IsMe {
			mine++
		}
	}
	return styleDim.Render(fmt.Sprintf(
		"%.0f%% · %d open · %d yours · %d milestones · %s to go back",
		m.drill.Progress*100, len(m.drillIssues), mine,
		len(milestoneSections(m.drillIssues)), m.keys.Back.Help().Key,
	))
}
