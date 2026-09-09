// The issue list's own side of the model: its rows, their rendering,
// its fetches and its cache. The chrome, the cursor, the search and
// the children are the model's, shared with the PR list.
package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fetchIssues asks the tracker for the open issues assigned to the user.
func (m model) fetchIssues() tea.Msg {
	gen := m.fetchGen
	if m.tracker == nil {
		return errMsg{gen, fmt.Errorf("no issue tracker configured (linear.token)")}
	}
	issues, err := m.tracker.Issues()
	if err != nil {
		return errMsg{gen, err}
	}
	return issuesMsg{gen, issues}
}

// fetchDone asks the tracker for what the user finished inside
// doneWindow. A failure here leaves the Done section empty rather than
// failing the list: the open issues are the point, this is the tail.
func (m model) fetchDone() tea.Msg {
	gen := m.fetchGen
	if m.tracker == nil {
		return doneMsg{gen, nil}
	}
	issues, err := m.tracker.Done(time.Now().Add(-doneWindow))
	if err != nil {
		return doneMsg{gen, nil}
	}
	return doneMsg{gen, issues}
}

// fetchIssuePRs lists the repo's open PRs, mine included: an issue's
// row shows the PRs opened for it.
func (m model) fetchIssuePRs() tea.Msg {
	gen := m.fetchGen
	if m.repo == "" {
		return issuePRsMsg{gen, nil} // no GitHub repo: the rows just have no PR
	}
	prs, err := ghPRList(m.repo, "open", "")
	if err != nil {
		return issuePRsMsg{gen, nil} // gh down or offline: the issues still list
	}
	return issuePRsMsg{gen, prs}
}

// byIssueKey indexes PRs by the issue keys their head branch carries,
// newest first — an issue may well have several open at once.
//
// By the key and not by Linear's branchName: only the branches owl
// itself creates carry that slug verbatim, and one PR in ten is pushed
// from it. A branch keeps the key and rewrites the slug —
// `bar-4159-company-fuzzy-match-particle-guard` for an issue Linear
// names `bar-4159-company-fuzzy-match-accepts-particle-only-name-overlap`
// — so matching the whole string finds almost nothing.
func byIssueKey(prs []PR) map[string][]PR {
	out := map[string][]PR{}
	for _, pr := range prs {
		for _, key := range issueKeysIn(pr.HeadRefName) {
			out[key] = append(out[key], pr)
		}
	}
	for _, list := range out {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Number > list[j].Number })
	}
	return out
}

// flattenPRs is the index back as the flat list the cache holds, each
// PR once however many issues it is filed under, newest first so the
// file does not churn on the map's iteration order.
func flattenPRs(index map[string][]PR) []PR {
	var out []PR
	seen := map[int]bool{}
	for _, list := range index {
		for _, pr := range list {
			if !seen[pr.Number] {
				seen[pr.Number] = true
				out = append(out, pr)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out
}

// doneWindow is how far back the Done section reaches: an issue you
// closed stays in view for a day, the way a merged PR does on the PR
// list, so finishing something does not make it vanish.
const (
	doneWindow      = 24 * time.Hour
	doneWindowLabel = "1d" // doneWindow, as the UI says it
)

// issueSections are the issue list's groups, in order: what is being
// worked on, what is next, what waits, what just finished.
var issueSections = []struct {
	title string
	note  string // dim, after the title
	style lipgloss.Style
	types []string // Linear's state types
}{
	{"In progress", "", styleSectionOK, []string{"started"}},
	{"Todo", "", styleSectionTodo, []string{"unstarted", "triage"}},
	{"Backlog", "", styleSectionWait, []string{"backlog"}},
	{"Done", doneWindowLabel, styleSectionMerged, []string{"completed"}},
}

// visibleIssueRows groups the issues by state type, newest change
// first within a group, with the search filter — a key, a title or a
// project fragment, case-insensitively — applied.
func (m model) visibleIssueRows() []visibleRow {
	filter := strings.ToLower(m.search.Value())
	matches := func(is Issue) bool {
		return filter == "" ||
			strings.Contains(strings.ToLower(is.Key), filter) ||
			strings.Contains(strings.ToLower(is.Title), filter) ||
			strings.Contains(strings.ToLower(is.Project.Name), filter)
	}
	// The open list and the done one are fetched apart; the sections
	// split them again by state type, and Issues() excludes what Done()
	// returns, so nothing lands twice.
	all := make([]Issue, 0, len(m.issues)+len(m.doneIssues))
	all = append(append(all, m.issues...), m.doneIssues...)
	var out []visibleRow
	for _, sec := range issueSections {
		var members []Issue
		for _, is := range all {
			if !contains(sec.types, is.State.Type) || !matches(is) {
				continue
			}
			// The window is the query's, but a cache read from an earlier
			// day would smuggle older ones in: hold the line here too.
			if is.State.Type == "completed" && time.Since(is.CompletedAt) > doneWindow {
				continue
			}
			members = append(members, is)
		}
		if len(members) == 0 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].UpdatedAt.After(members[j].UpdatedAt) })
		out = append(out, visibleRow{sectionTitle: sec.title, sectionNote: sec.note, sectionStyle: sec.style})
		for i := range members {
			out = append(out, visibleRow{issue: &members[i], sectionTitle: sec.title})
		}
	}
	return out
}

// milestoneSection is a project's issues under one of its milestones.
type milestoneSection struct {
	title  string
	issues []Issue
}

// milestoneSections groups a project's issues by milestone, in the
// project's own order — the milestone's sortOrder, not the alphabet —
// with the unmilestoned last under a heading of their own.
//
// That last section is often the biggest and is not a mistake: issues
// are milestoned as a project's plan firms up, not when they are
// filed. Sequential Capture redesign has 49 of its 52 open issues on a
// milestone; Sven v2 has one of eight.
func milestoneSections(issues []Issue) []milestoneSection {
	type group struct {
		name   string
		order  float64
		issues []Issue
	}
	byID := map[string]*group{}
	var ids []string
	for _, is := range issues {
		g, ok := byID[is.Milestone.ID]
		if !ok {
			g = &group{name: is.Milestone.Name, order: is.Milestone.SortOrder}
			byID[is.Milestone.ID] = g
			ids = append(ids, is.Milestone.ID)
		}
		g.issues = append(g.issues, is)
	}
	sort.SliceStable(ids, func(i, j int) bool {
		if (ids[i] == "") != (ids[j] == "") {
			return ids[j] == "" // the unmilestoned go last
		}
		return byID[ids[i]].order < byID[ids[j]].order
	})
	out := make([]milestoneSection, 0, len(ids))
	for _, id := range ids {
		g := byID[id]
		title := g.name
		if id == "" {
			title = "No milestone"
		}
		sort.SliceStable(g.issues, func(i, j int) bool { return g.issues[i].UpdatedAt.After(g.issues[j].UpdatedAt) })
		out = append(out, milestoneSection{title: title, issues: g.issues})
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// The row's columns. Everything but the title is fixed width, so the
// eye can run down a column; the title takes whatever the window has
// left, and the PR chips ride at the end.
const (
	projectWidth  = 18
	stateWidth    = 11 // "In Progress", the longest of Linear's defaults
	assigneeWidth = 9  // a given name, for the rows that are not yours
	titleFloor    = 24
	titleCeiling  = 80
	// fixedWidth is everything else on the row, chips included: cursor,
	// key, badges, priority, age, the gaps, and room for two chips.
	fixedWidth = 2 + 10 + 5 + 4 + 5 + 2 + projectWidth + 2 + stateWidth + 1 + assigneeWidth + 14
)

// titleWidth is what the title gets in this window. Without a size yet
// (a test that never sent one), the old fixed 60.
func (m model) titleWidth() int {
	if m.width == 0 {
		return 60
	}
	// Drilled, the project column is gone and the title has its width.
	return clampInt(m.width-fixedWidth+(projectWidth-m.projectColumn()), titleFloor, titleCeiling)
}

// projectColumn is the width of the project column: none while
// drilled into a project, since every row would name it.
func (m model) projectColumn() int {
	if m.drill != nil {
		return 0
	}
	return projectWidth
}

// renderIssueRow: cursor, key, the workspace badges, priority, age,
// title, project, state, and the issue's open PRs when there are any.
//
// The state name only when it says something the section does not: in
// Backlog every row would read "Backlog", while inside In progress the
// difference between "In Progress" and "In Review" is the point.
func (m model) renderIssueRow(row visibleRow, selected bool) string {
	is := row.issue
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	local := m.localOf(row)
	starting := ""
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	// A done row shows the age of the closing, not of the last edit —
	// the section already says it is done, as the PR list's merged rows
	// show the merge age.
	when := is.UpdatedAt
	if is.State.Type == "completed" && !is.CompletedAt.IsZero() {
		when = is.CompletedAt
	}
	state := stateUnlessSection(is.State.Name, row.sectionTitle)
	// Whose it is, blank when it is yours — so in a project's list the
	// gaps down this column are your own queue, and in the assigned
	// list, where every issue is yours, the column costs nothing.
	who := ""
	if !is.Assignee.IsMe {
		who = firstWord(is.Assignee.Name)
	}
	// Not inside a project: every row would name the one you drilled
	// into, and the title wants those columns more.
	project := ""
	if m.drill == nil {
		project = is.Project.Name
	}
	w := m.titleWidth()
	return strings.TrimRight(fmt.Sprintf(
		"%s%-9s %s %3s %s  %-*s  %s %s%s%s",
		cursor,
		is.Key,
		workspaceBadges(local, starting),
		priorityMark(is.Priority),
		styleDim.Render(fmt.Sprintf("%3s", relativeAge(when.Format(time.RFC3339)))),
		w, trim(is.Title, w),
		cell(project, m.projectColumn(), styleDim),
		cell(state, stateWidth, styleDim),
		cell(who, assigneeWidth, styleDim),
		prChips(m.issuePRs[is.Key]),
	), " ")
}

// cell is a fixed-width column with the text styled and the padding
// left plain, so a row whose last columns are empty ends in spaces the
// caller can trim rather than in a run of dimmed blanks.
func cell(s string, w int, style lipgloss.Style) string {
	s = trim(s, w)
	pad := strings.Repeat(" ", w-len([]rune(s)))
	if s == "" {
		return pad
	}
	return style.Render(s) + pad
}

// workspaceBadges is the issue row's two-slot block: the worktree (or
// the spinner while a child works on it) and the agent's state, as
// the PR row's first two slots.
func workspaceBadges(ls LocalState, starting string) string {
	var parts []string
	switch {
	case starting != "":
		parts = append(parts, starting)
	case ls.Worktree != "":
		parts = append(parts, styleWorktree.Render("⎇"))
	default:
		parts = append(parts, " ")
	}
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
	return strings.Join(parts, " ")
}

// prChips names every open PR on the issue, newest first. Nothing when
// it has none.
func prChips(prs []PR) string {
	var b strings.Builder
	for _, pr := range prs {
		b.WriteString("  " + prChip(pr))
	}
	return b.String()
}

// prChip names one PR: `#N`, dim while a draft, with ⚠ when a reviewer
// requested changes and ✓ when one approved.
func prChip(pr PR) string {
	chip := fmt.Sprintf("#%d", pr.Number)
	switch {
	case pr.HasChangesRequested():
		return styleChangesReqd.Render(chip + "⚠")
	case pr.anyApproved():
		return styleApproved.Render(chip + "✓")
	case pr.IsDraft:
		return styleDraft.Render(chip + " draft")
	}
	return chip
}

// anyApproved reports whether any reviewer's latest verdict approves.
func (p PR) anyApproved() bool {
	for _, r := range p.latestVerdictsPerUser() {
		if r.State == "APPROVED" {
			return true
		}
	}
	return false
}

// issueCountsSummary is the idle-state action row of the issue list.
func (m model) issueCountsSummary() string {
	counts := map[string]int{}
	for _, is := range m.issues {
		counts[is.State.Type]++
	}
	return styleDim.Render(fmt.Sprintf(
		"%d open · %d in progress · %d todo · %d backlog",
		len(m.issues),
		counts["started"],
		counts["unstarted"]+counts["triage"],
		counts["backlog"],
	))
}

// issueLegend explains the issue row's glyphs.
func (m model) issueLegend() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   worktree present on the issue's branch", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude working — actively processing a turn", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s   Claude blocked — waiting on you (permission, question, plan approval)", styleClaudeBlocked.Render("©")),
		fmt.Sprintf("  %s  Claude done — unread (result to view; ack by focusing the window)", styleClaudeDone.Render("©")+styleClaudeDone.Render("*")),
		fmt.Sprintf("  %s   Claude idle — finished and seen (session still available)", styleClaudeDone.Render("©")),
		fmt.Sprintf("  %s   Claude session — no state set (fresh window)", styleClaudeNeutral.Render("©")),
		"  !!! !! ! -   priority: urgent, high, medium, low",
		fmt.Sprintf("  %s %s %s   the issue's open PRs, newest first: approved, changes requested, draft", styleApproved.Render("#N✓"), styleChangesReqd.Render("#N⚠"), styleDraft.Render("#N draft")),
	)
}
