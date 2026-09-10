// The `mine` pane: your own open pull requests, grouped by what is
// stopping each from landing.
//
// A different question from the review list's, and so a different
// shape. That one asks where you sit on someone else's work and groups
// by your verdict; this one asks what is in the way of yours — a red
// check, a reviewer who wants changes, a reviewer nobody asked. The
// rows carry GitHub's own answers rather than derived ones:
// reviewDecision is what the merge button reads, and the rollup is the
// head commit's.
package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fetchMine asks GitHub for your own open PRs.
func (m model) fetchMine() tea.Msg {
	gen := m.fetchGen
	if m.repo == "" {
		return mineMsg{gen, nil}
	}
	prs, err := minePRs(m.repo)
	if err != nil {
		return errMsg{gen, err}
	}
	return mineMsg{gen, prs}
}

// blocking is why a PR of yours is not landing, and which section it
// belongs to. The order is the order of the sections.
type blocking int

const (
	blockedOnYou    blocking = iota // a red check, changes requested, a conflict
	readyToMerge                    // approved and green: nothing left but the button
	waitingOnOthers                 // reviewers asked, none have answered
	notOutForReview                 // never offered to anyone: a draft, or one you forgot to ask about
)

// whyBlocked places one of your PRs.
//
// Mergeable is read where GitHub has bothered to compute it — it is
// UNKNOWN on most PRs most of the time, since it is worked out lazily
// when something asks — so a conflict counts when it is seen and is
// never the reason a section is empty.
func whyBlocked(pr PR) blocking {
	switch {
	case pr.CI == "FAILURE" || pr.CI == "ERROR",
		pr.ReviewDecision == "CHANGES_REQUESTED",
		pr.Mergeable == "CONFLICTING":
		return blockedOnYou
	case pr.ReviewDecision == "APPROVED" && !pr.IsDraft:
		return readyToMerge
	case pr.Asked > 0:
		return waitingOnOthers
	}
	// Nobody has been asked. On a draft that is the definition; on one
	// marked ready it is the omission this pane can catch. Both are your
	// move and neither is out for review, so they share a section — the
	// [draft] on the row says which move it is, and the sort puts the
	// omission on top.
	return notOutForReview
}

// mineSections are the groups of the mine pane, in order.
var mineSections = []struct {
	title string
	style lipgloss.Style
	why   blocking
}{
	{"Blocked on you", styleSectionTodo, blockedOnYou},
	{"Ready to merge", styleSectionOK, readyToMerge},
	{"Waiting on reviewers", styleSectionWait, waitingOnOthers},
	{"Not out for review", styleSectionWait, notOutForReview},
}

// visibleMineRows groups your PRs by what is blocking them, newest
// change first, with the search filter applied.
func (m model) visibleMineRows() []visibleRow {
	filter := m.search.Value()
	var out []visibleRow
	for _, sec := range mineSections {
		var members []PR
		for _, pr := range m.mine {
			if whyBlocked(pr) != sec.why {
				continue
			}
			if filter != "" && !strings.Contains(strconv.Itoa(pr.Number), filter) && !strings.Contains(strings.ToLower(pr.Title), strings.ToLower(filter)) {
				continue
			}
			members = append(members, pr)
		}
		if len(members) == 0 {
			continue
		}
		sort.SliceStable(members, func(i, j int) bool {
			// Drafts sink, in the one section that mixes them with
			// something you meant to send out. Elsewhere a draft is
			// ordinary and recency is the only rule.
			if sec.why == notOutForReview && members[i].IsDraft != members[j].IsDraft {
				return !members[i].IsDraft
			}
			return parsedTime(members[i].UpdatedAt).After(parsedTime(members[j].UpdatedAt))
		})
		out = append(out, visibleRow{sectionTitle: sec.title, sectionStyle: sec.style})
		for i := range members {
			out = append(out, visibleRow{pr: &members[i], sectionTitle: sec.title, mine: true})
		}
	}
	return out
}

// mineBadges is the state of one of your PRs in a fixed block: the
// review verdict, then CI, then a conflict where GitHub has computed
// one.
func mineBadges(pr PR) string {
	verdict := " "
	switch pr.ReviewDecision {
	case "APPROVED":
		verdict = styleApproved.Render("✓")
	case "CHANGES_REQUESTED":
		verdict = styleChangesReqd.Render("⚠")
	case "REVIEW_REQUIRED":
		if pr.Asked > 0 {
			verdict = styleDim.Render("·")
		} else {
			verdict = styleDim.Render("○")
		}
	}
	ci := " "
	switch pr.CI {
	case "SUCCESS":
		ci = styleApproved.Render("✓")
	case "FAILURE", "ERROR":
		ci = styleChangesReqd.Render("✗")
	case "PENDING", "EXPECTED":
		ci = styleDraft.Render("◐")
	}
	conflict := " "
	if pr.Mergeable == "CONFLICTING" {
		conflict = styleChangesReqd.Render("⚠")
	}
	return verdict + " " + ci + " " + conflict
}

// issueChips names every issue of yours a PR's branch carries, newest
// first — the mirror of prChips on an issue row, where a PR appears
// under several issues for the same reason.
//
// The first is the one whose workspace the PR opens into: a branch
// closing two issues is one piece of work and gets one workspace, and
// the newest is the one being worked on.
func issueChips(keys []string) string {
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(styleDim.Render("  " + key))
	}
	return b.String()
}

// mineLegend explains the mine row's glyphs. Three fixed slots — the
// review verdict, the head commit's checks, a conflict — and then the
// issues the branch carries.
func (m model) mineLegend() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   a worktree exists for this row — your branch's, wherever it is", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude working — actively processing a turn", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s   Claude blocked — waiting on you (permission, question, plan approval)", styleClaudeBlocked.Render("©")),
		fmt.Sprintf("  %s  Claude done — unread (result to view; ack by focusing the window)", styleClaudeDone.Render("©")+styleClaudeDone.Render("*")),
		"",
		fmt.Sprintf("  %s   approved — GitHub's own reviewDecision, what the merge button reads", styleApproved.Render("✓")),
		fmt.Sprintf("  %s   changes requested by a reviewer", styleChangesReqd.Render("⚠")),
		fmt.Sprintf("  %s   reviewers asked, none have answered", styleDim.Render("·")),
		fmt.Sprintf("  %s   nobody has been asked to look", styleDim.Render("○")),
		"",
		fmt.Sprintf("  %s %s %s the head commit's checks: green, failing, still running", styleApproved.Render("✓"), styleChangesReqd.Render("✗"), styleDraft.Render("◐")),
		fmt.Sprintf("  %s   third slot: GitHub reports the branch as conflicting", styleChangesReqd.Render("⚠")),
		fmt.Sprintf("  %s   the issues the branch carries, newest first — the first names the workspace", styleDim.Render("BAR-4157")),
	)
}

// mineCountsSummary is the action row of the mine pane.
func (m model) mineCountsSummary() string {
	counts := map[blocking]int{}
	drafts := 0
	for _, pr := range m.mine {
		counts[whyBlocked(pr)]++
		if pr.IsDraft {
			drafts++
		}
	}
	return styleDim.Render(fmt.Sprintf(
		"%d yours · %d blocked · %d ready · %d waiting · %d draft",
		len(m.mine), counts[blockedOnYou], counts[readyToMerge],
		counts[waitingOnOthers], drafts,
	))
}
