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

// issueSections are the issue list's groups, in order: what is being
// worked on, what is next, what waits.
var issueSections = []struct {
	title string
	style lipgloss.Style
	types []string // Linear's state types
}{
	{"In progress", styleSectionOK, []string{"started"}},
	{"Todo", styleSectionTodo, []string{"unstarted", "triage"}},
	{"Backlog", styleSectionWait, []string{"backlog"}},
}

// visibleIssueRows groups the issues by state type, newest change
// first within a group, with the search filter — a key or a title
// fragment, case-insensitively — applied.
func (m model) visibleIssueRows() []visibleRow {
	filter := strings.ToLower(m.search.Value())
	matches := func(is Issue) bool {
		return filter == "" || strings.Contains(strings.ToLower(is.Key), filter) || strings.Contains(strings.ToLower(is.Title), filter)
	}
	var out []visibleRow
	for _, sec := range issueSections {
		var members []Issue
		for _, is := range m.issues {
			if !contains(sec.types, is.State.Type) || !matches(is) {
				continue
			}
			members = append(members, is)
		}
		if len(members) == 0 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool { return members[i].UpdatedAt.After(members[j].UpdatedAt) })
		out = append(out, visibleRow{sectionTitle: sec.title, sectionStyle: sec.style})
		for i := range members {
			out = append(out, visibleRow{issue: &members[i]})
		}
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

// renderIssueRow: cursor, key, the workspace badges, priority, age,
// title, state, and the issue's open PRs when there are any.
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
	age := relativeAge(is.UpdatedAt.Format(time.RFC3339))
	return fmt.Sprintf(
		"%s%-9s %s %3s %s  %s  %s%s",
		cursor,
		is.Key,
		workspaceBadges(local, starting),
		priorityMark(is.Priority),
		styleDim.Render(fmt.Sprintf("%3s", age)),
		trim(is.Title, 60),
		styleDim.Render(is.State.Name),
		prChips(m.issuePRs[is.Key]),
	)
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
