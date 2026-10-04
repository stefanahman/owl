package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ------------------------------------------------------------
// Content rendering
// ------------------------------------------------------------

// visibleRows builds the section-grouped row list with the search
// filter applied. Sections with zero visible rows are omitted.
func (m model) visibleRows() []visibleRow {
	if m.drill != nil {
		return m.visibleDrillRows()
	}
	switch m.kind {
	case "issue":
		return m.visibleIssueRows()
	case "project":
		return m.visibleProjectRows()
	}
	// The PR list has two panes and the focused one owns the cursor, so
	// every caller of this — selectedRow, clampCursor, the scroll maths
	// — reads the pane you are driving without knowing there are two.
	if m.mineFocus {
		return m.visibleMineRows()
	}
	return m.visibleReviewRows()
}

// refreshList slices the visible rows to fit the viewport and pushes
// them into it via SetContent. Slice-based rendering matches the
// pattern in bubbles/table.UpdateViewport: content in the viewport is
// always at YOffset 0; scrolling = re-slicing on cursor move.
func (m *model) refreshList() {
	m.focusWhereTheRowsAre()
	rows := m.visibleRows()
	h := m.contentHeight
	if m.panes() {
		h = m.focusedPaneHeight()
	}
	// Set it even at zero. resizeViewport gives the viewport the whole
	// content height and leaves the narrowing to here, so skipping the
	// call when the focused pane has no rows left it at full height —
	// a screenful of blank lines, and the other pane pushed off the
	// bottom. That is what an empty review queue looked like.
	if h < 0 {
		h = 0
	}
	m.list.SetHeight(h)
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
	if row.header() {
		if row.sectionNote != "" {
			return row.sectionStyle.Render(row.sectionTitle) + styleDim.Render(" · "+row.sectionNote)
		}
		return row.sectionStyle.Render(row.sectionTitle)
	}
	if row.issue != nil {
		return m.renderIssueRow(row, selected)
	}
	if row.project != nil {
		return m.renderProjectRow(row, selected)
	}
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	local := m.localOf(row)
	starting := "" // the spinner takes the worktree slot while a child works on this PR
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	draft := ""
	if row.pr.IsDraft {
		draft = styleDraft.Render(" [draft]")
	}
	// The age sits in a three-cell column before the title, where the
	// eye already is; after a long title it landed far to the right.
	// Merged rows show the merge age — the section says merged.
	age := relativeAge(row.pr.UpdatedAt)
	if row.merged {
		age = relativeAge(row.pr.MergedAt)
	}
	// Your own PR carries a different set of facts and so a different
	// block: GitHub's verdict, the head commit's checks, and a conflict
	// where it has computed one. The author is you, so the name goes.
	if row.mine {
		// The issues this branch closes, which is also where Enter takes
		// you. The title gives up the width they need, down to a floor —
		// a row with three keys on it still has to read as a title.
		chips := issueChips(issueKeysFor(row.pr.HeadRefName, m.cfg.Linear.TeamKeys()))
		repo := m.repoCell(row.pr)
		width := max(68-lipgloss.Width(chips)-lipgloss.Width(repo), 24)
		return strings.TrimRight(fmt.Sprintf(
			"%s#%-5d %s %s  %s %s%s%s%s",
			cursor,
			row.pr.Number,
			workspaceBadges(local, starting),
			mineBadges(*row.pr),
			styleDim.Render(fmt.Sprintf("%3s", age)),
			repo,
			trim(row.pr.Title, width),
			draft,
			chips,
		), " ")
	}
	repo := m.repoCell(row.pr)
	out := fmt.Sprintf(
		"%s#%-5d %s %s  %s%s%s (%s)",
		cursor,
		row.pr.Number,
		badges(local, starting, verdictOf(row, m.me)),
		styleDim.Render(fmt.Sprintf("%3s", age)),
		repo,
		trim(row.pr.Title, 70-lipgloss.Width(repo)),
		draft,
		row.pr.Author.Login,
	)
	// Asked of your team and not of you: present, with a lower claim on
	// you, which is what dim means everywhere else in owl. Stripped of
	// its colours first — dimming over them leaves the escape codes
	// fighting and the row bright.
	if row.pr.teamAskedNotYou(m.me) {
		out = dimBlock(out)
	}
	return out
}

// ------------------------------------------------------------
// View
// ------------------------------------------------------------

// actionRowView renders the single-line row under the title. It's
// always present (chrome math is simpler that way) and shows one of:
//   - the spinner + what is in flight ("opening #42…") while children run
//   - the loading spinner + "loading PRs…" while the initial fetch is pending
//   - the search input when the user is typing
//   - the filter chip when a filter is applied but the input is blurred
//   - the last action's failure, or a fetch failure while the list is stale
//   - the counts summary otherwise
//
// Never more than one visible line — length may exceed width and be
// truncated by the terminal; that's acceptable for a status row.
func (m model) actionRowView() string {
	switch {
	case len(m.inflight) > 0:
		labels := make([]string, 0, len(m.inflight))
		for _, pr := range slices.Sorted(maps.Keys(m.inflight)) {
			labels = append(labels, m.inflight[pr])
		}
		return m.spinner.View() + " " + styleDim.Render(strings.Join(labels, "  "))
	case !m.ready && m.err == nil:
		return m.spinner.View() + " " + styleDim.Render("loading "+m.noun()+"…")
	case m.refreshing:
		return m.spinner.View() + " " + styleDim.Render("refreshing…")
	case m.search.Focused():
		return styleSearchLabel.Render("search /") + m.search.View()
	case m.search.Value() != "":
		return styleSearchLabel.Render("filter /"+m.search.Value()) +
			styleDim.Render("   [/] edit   [esc] clear")
	case m.notice != nil:
		return styleChangesReqd.Render("error: " + m.notice.Error())
	case m.err != nil && m.hasData():
		// The list is still the last good fetch; say so instead of
		// replacing it (offline with a warm cache is the common case).
		return styleChangesReqd.Render("error: " + m.err.Error())
	default:
		return m.countsSummary()
	}
}

// panes reports whether the list is the two-pane one. Only the PR
// list is: a review queue and your own PRs are different questions
// with different answers and different keys.
func (m model) panes() bool { return m.kind == "pr" && m.drill == nil }

// hasData reports whether there is a list to show — from a fetch or
// the cache.
// What just finished counts as data: a week where the only project you
// touched is one you closed is still a list, not a loading screen.
func (m model) hasData() bool {
	if m.drill != nil {
		return len(m.drillIssues) > 0
	}
	switch m.kind {
	case "issue":
		return len(m.issues) > 0 || len(m.doneIssues) > 0 || len(m.cancelledIssues) > 0
	case "project":
		return len(m.projects) > 0 || len(m.doneProjects) > 0
	}
	return len(m.prs) > 0 || len(m.merged) > 0 || len(m.mine) > 0
}

// noun is what the list holds, plural: PRs, issues or projects.
func (m model) noun() string {
	if m.drill != nil {
		return "issues"
	}
	switch m.kind {
	case "issue":
		return "issues"
	case "project":
		return "projects"
	}
	return "PRs"
}

// countsSummary is the idle-state action row content: a compact
// count-per-group line so a glance tells you today's shape.
func (m model) countsSummary() string {
	if m.drill != nil {
		return m.drillCountsSummary()
	}
	switch m.kind {
	case "issue":
		return m.issueCountsSummary()
	case "project":
		return m.projectCountsSummary()
	}
	if m.me == "" || len(m.prs) == 0 && len(m.merged) == 0 && len(m.mine) == 0 {
		return styleDim.Render(fmt.Sprintf("%d open", len(m.prs)))
	}
	if m.mineFocus {
		return m.mineCountsSummary()
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
		m.cfg.MergedWindow.Text,
	))
}

// titleLine renders the header row: what the list is scoped by,
// left-aligned, `updated Xm ago` right-aligned, padded to fill
// m.width. The timestamp is omitted before the first fetch completes
// (lastFetched is zero).
func (m model) titleLine(repo string) string {
	// Every list says what it is scoped by, and each is scoped by
	// something different: the PR list by the repositories it spans,
	// the others by the Linear workspaces they read. One of them is
	// named, several are all named — a title naming one of several is
	// worse than a title naming none.
	//
	// The PR list is owl's front door and leads with its scope alone;
	// the others name themselves first, from the same noun the
	// empty-search line uses, so the two can never disagree.
	left := styleHeader.Render(fmt.Sprintf("owl · %s", m.prScopeLabel(repo)))
	if m.drill != nil {
		// The project, not the repo: while drilled that is where you are.
		left = styleHeader.Render(fmt.Sprintf("owl · %s", trim(m.drill.Name, 48)))
	} else if m.kind != "pr" {
		// The issue and project lists are not scoped by a repo — they are
		// what is assigned to you and what you work in — so where several
		// Linear workspaces answer, the workspaces are the honest scope.
		// A repo there would name one of them while the rows come from
		// both. With one workspace the repo stays: it is the context you
		// are standing in, and a lone workspace is often not even named.
		scope := repo
		if names := m.workspaceNames(); names != "" {
			scope = names
		}
		left = styleHeader.Render(fmt.Sprintf("owl · %s · %s", m.noun(), scope))
	}
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
	title := styleHeader.Render("owl · help")

	legend := m.legend()

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

// legend explains the glyphs of the list's rows.
func (m model) legend() string {
	switch m.kind {
	case "issue":
		return m.issueLegend()
	case "project":
		return m.projectLegend()
	}
	// The two panes of the PR list carry different glyphs because they
	// answer different questions, so the legend follows the focus the
	// way the keys do.
	if m.mineFocus {
		return m.mineLegend()
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		styleHeader.Render("Legend"),
		fmt.Sprintf("  %s   a worktree exists for this row", styleWorktree.Render("⎇")),
		fmt.Sprintf("  %s   Claude working — actively processing a turn", styleClaudeWorking.Render("©")),
		fmt.Sprintf("  %s   Claude blocked — waiting on you (permission, question, plan approval)", styleClaudeBlocked.Render("©")),
		fmt.Sprintf("  %s  Claude done — unread (result to view; ack by focusing the window)", styleClaudeDone.Render("©")+styleClaudeDone.Render("*")),
		fmt.Sprintf("  %s   Claude idle — finished and seen (session still available)", styleClaudeDone.Render("©")),
		fmt.Sprintf("  %s   Claude session — no state set (fresh window)", styleClaudeNeutral.Render("©")),
		fmt.Sprintf("  %s   I approved this PR (current verdict)", styleApproved.Render("✓")),
		fmt.Sprintf("  %s   I engaged — commented or requested changes, no approval", styleDim.Render("·")),
		fmt.Sprintf("  %s %s the author pushed after that review — it no longer covers the head", styleReviewStale.Render("✓"), styleReviewStale.Render("·")),
		fmt.Sprintf("  %s   changes requested by any reviewer (PR blocked)", styleChangesReqd.Render("⚠")),
		"",
		styleDim.Render("  a dim row was asked of a team you are in, not of you — `n` walks past it"),
	)
}

// View wraps the rendered frame with what used to be program options:
// the alternate screen and focus reporting (auto-refresh on focus).
// The search input draws its own cursor.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.ReportFocus = true
	return v
}

func (m model) render() string {
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

	if m.err != nil && !m.hasData() {
		b.WriteString(fmt.Sprintf("\nerror: %v\n", m.err))
		b.WriteString("\n" + m.help.View(m.keys))
		return b.String()
	}

	if !m.ready {
		// Loading state is already shown in the action row (spinner + text).
		// Leave the body blank so the eye stays where the movement is.
		return b.String()
	}

	rows := m.visibleRows()
	// Empty means both panes on the PR list, not the focused one. Your
	// own pull requests live in the other pane, and a repo where every
	// PR is yours — which a solo project is — has nothing to review and
	// everything to show. Returning here would draw "no PRs need your
	// review" over a pane that was holding them, and tab cannot reach a
	// pane that was never rendered.
	empty := len(rows) == 0
	if m.panes() {
		empty = empty && len(m.otherPaneRows()) == 0
	}
	if empty {
		switch {
		case m.search.Value() != "":
			b.WriteString(fmt.Sprintf("\nno %s match /%s\n", m.noun(), m.search.Value()))
		case m.kind == "project":
			b.WriteString("\nno open projects you work in.\n")
		case m.kind == "issue":
			b.WriteString("\nno open issues assigned to you.\n")
		default:
			b.WriteString("\nno PRs need your review.\n")
		}
		b.WriteString("\n" + m.help.View(m.keys))
		return b.String()
	}

	if m.panes() {
		b.WriteString(m.panesView())
	} else {
		b.WriteString(m.list.View())
	}
	b.WriteString("\n\n" + m.help.View(m.keys))
	return b.String()
}

// ------------------------------------------------------------
// Badges & helpers
// ------------------------------------------------------------

// trim shortens s to at most n runes, replacing the tail with an
// ellipsis if it was cut. Runes, not bytes: slicing a title on a byte
// boundary inside "—" or an emoji emits a broken UTF-8 sequence.
func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// relativeAge formats an RFC3339 timestamp as "now", "5m", "3h", "2d"
// or "3w" — three cells at most.
func relativeAge(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
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

// scopeLabel names the sources a list spans, for its title. Joined by
// a plus rather than a comma: a title saying two things is saying both
// are in the list, not that it holds one or the other.
//
// Shared by the two lists that can span something — the PR list over
// repository owners, the issue and project lists over Linear
// workspaces — which are different enough not to share more than this.
func scopeLabel(names []string) string {
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if n != "" {
			kept = append(kept, n)
		}
	}
	return strings.Join(kept, " + ")
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
