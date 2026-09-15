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
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// fetchMine asks GitHub for your own PRs: the open ones, and the ones
// that landed inside merged_window.
//
// The merged half is asked for separately because it answers a
// different question and GitHub answers it with a different query —
// and because nothing else shows it. The review list excludes your own
// PRs by author (you cannot review your own), and the open fetch drops
// a PR the moment it merges, so without this your own merge disappears
// the second it lands.
func (m model) fetchMine() tea.Msg {
	gen := m.fetchGen
	if m.repo == "" {
		return mineMsg{gen: gen}
	}
	prs, err := minePRs(m.repo)
	if err != nil {
		return errMsg{gen, err}
	}
	// A failure here leaves the Merged section empty rather than
	// failing the pane: the open PRs are the pane's job, and what
	// landed is the footnote.
	cutoff := time.Now().Add(-m.cfg.MergedWindow.D).UTC().Format("2006-01-02T15:04:05Z")
	merged, err := ghPRList(m.repo, "merged", fmt.Sprintf("author:@me merged:>=%s", cutoff))
	if err != nil {
		merged = nil
	}
	return mineMsg{gen: gen, prs: prs, merged: merged}
}

// blocking is why a PR of yours is not landing, and which section it
// belongs to. The order is the order of the sections.
type blocking int

const (
	blockedOnYou    blocking = iota // a red check, a conflict, changes you have not handed back
	readyToMerge                    // approved and green: nothing left but the button
	waitingOnOthers                 // reviewers asked, none have answered
	notOutForReview                 // never offered to anyone: a draft, or one you forgot to ask about
)

// whyBlocked places one of your PRs by what is stopping it, which is
// not the same question as what GitHub will let you merge.
//
// Mergeable is read where GitHub has bothered to compute it — it is
// UNKNOWN on most PRs most of the time, since it is worked out lazily
// when something asks — so a conflict counts when it is seen and is
// never the reason a section is empty.
func whyBlocked(pr PR) blocking {
	switch {
	// A red check and a conflict are yours whoever is reviewing.
	case pr.CI == "FAILURE" || pr.CI == "ERROR",
		pr.Mergeable == "CONFLICTING":
		return blockedOnYou
	// Changes requested and still in your hands. Once you have handed
	// it back it is theirs again, and a request outstanding is what
	// says you have: a reviewer's own review consumes their request, so
	// one standing alongside their verdict was made after it.
	//
	// GitHub disagrees and keeps reviewDecision at CHANGES_REQUESTED
	// until they answer — re-requesting does not clear it, only a new
	// review or a dismissal does, and mergeStateStatus stays BLOCKED
	// with it. That is the right answer to "may this merge" and the
	// wrong one to "whose move is it", which is what this pane asks.
	// The row still carries the ⚠, so the verdict is not hidden by the
	// section it sits in.
	case pr.ReviewDecision == "CHANGES_REQUESTED" && pr.Asked == 0:
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
	// What landed, last: the pane is about what is in the way, and
	// these are in nobody's way. They are here because they are
	// nowhere else — the review list is other people's work by
	// definition — so "did that land?" has an answer that does not
	// involve leaving owl.
	var landed []PR
	for _, pr := range m.mineMerged {
		if filter != "" && !strings.Contains(strconv.Itoa(pr.Number), filter) && !strings.Contains(strings.ToLower(pr.Title), strings.ToLower(filter)) {
			continue
		}
		landed = append(landed, pr)
	}
	if len(landed) == 0 {
		return out
	}
	sort.SliceStable(landed, func(i, j int) bool {
		return parsedTime(landed[i].MergedAt).After(parsedTime(landed[j].MergedAt))
	})
	title := "Merged (last " + m.cfg.MergedWindow.Text + ")"
	out = append(out, visibleRow{sectionTitle: title, sectionStyle: styleSectionMerged, merged: true})
	for i := range landed {
		out = append(out, visibleRow{pr: &landed[i], sectionTitle: title, mine: true, merged: true})
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
