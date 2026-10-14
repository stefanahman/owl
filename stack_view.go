// The stack view: → on a stacked row of the PR list shows the stacks
// through it in place of the list, and ← goes back. A line row opens
// the stack review of that line; a layer row opens the layer's own
// review, as the list does.
package main

import (
	"encoding/json"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// stackView is the stack view's state: whose stacks, and where to go
// back to.
type stackView struct {
	repo string // owner/name
	at   int    // the pull request it was opened on
	back int    // the list's cursor, to come back to
	// filter is the list's search, put aside while the view shows: the
	// view does not filter, and the cursor it comes back to is a row
	// of the filtered list.
	filter string
	// layers are the details the graph does not carry, fetched when the
	// view opens: the reviews, the checks, the last update. Until they
	// land a layer row is drawn from the graph alone.
	layers map[int]PR
	// recorded is the graph of a line GitHub no longer holds open, as
	// the records of the stack workspaces on it have it: the view of a
	// merged layer. nil for an open stack, which the list's graphs hold.
	recorded *prGraph
}

// stackLines are the lines through at, one per tip above it: what a
// stack review can take.
func stackLines(g *prGraph, at int) []stackLine {
	l, err := g.line(at)
	if err != nil {
		return nil
	}
	if len(l.Tips) == 0 {
		return []stackLine{l}
	}
	var out []stackLine
	for _, t := range l.Tips {
		if tl, err := g.line(t.Number); err == nil {
			out = append(out, tl)
		}
	}
	return out
}

// stackGraph is the graph the view shows, or nil.
func (m model) stackGraph() *prGraph {
	if m.stack == nil {
		return nil
	}
	if m.stack.recorded != nil {
		return m.stack.recorded
	}
	return m.stacks[m.stack.repo]
}

// recordedGraph is the line merged pr was a layer of, as the records of
// the stack workspaces still holding it have it — the workspaces whose
// badges its row draws (stackReview), so → goes where they say. GitHub
// keeps no line once its layers merge: when one merges on its own, the
// layer above is retargeted to the trunk. The record's order is the line;
// a layer's title and branch are what the list knows of it, until the
// view's fetch of the layers lands. nil when no workspace holds pr.
func (m model) recordedGraph(pr *PR) *prGraph {
	repo := pr.Repo
	if repo == "" {
		repo = m.repo
	}
	trunk := ""
	if g := m.graphOf(pr); g != nil {
		trunk = g.trunk
	}
	known := map[int]PR{}
	for _, list := range [][]PR{m.prs, m.merged, m.mine, m.mineMerged} {
		for _, p := range list {
			known[p.Number] = p
		}
	}
	layers := map[int]stackPR{}
	for _, r := range m.stackRecords {
		if !strings.EqualFold(r.Repo, repo) || !slices.Contains(r.PRs, pr.Number) {
			continue
		}
		if ls, ok := m.localState[r.name]; !ok || ls.Worktree == "" && ls.Window == "" {
			continue // a closed stack keeps its record, and holds nothing
		}
		// Each layer on the head of the one below it: the base GitHub
		// holds now may be the trunk it was retargeted to.
		base := trunk
		for _, n := range r.PRs {
			sp, ok := layers[n]
			if !ok {
				p := known[n]
				sp = stackPR{Number: n, Title: p.Title, URL: p.URL, HeadRefName: p.HeadRefName,
					HeadRefOid: p.HeadRefOid, IsDraft: p.IsDraft}
				sp.Author.Login = p.Author.Login
				if sp.HeadRefName == "" || sp.HeadRefName == trunk {
					sp.HeadRefName = fmt.Sprintf("pull/%d/head", n) // unknown here, and only a link
				}
			}
			sp.BaseRefName = base
			layers[n] = sp
			base = sp.HeadRefName
		}
	}
	if len(layers) == 0 {
		return nil
	}
	return newPRGraph(repo, trunk, slices.Collect(maps.Values(layers)))
}

// layerPR is a layer as a row shows it: the fetched details where they
// have landed, else what the graph knows.
func (m model) layerPR(g *prGraph, n int) PR {
	if pr, ok := m.stack.layers[n]; ok {
		return pr
	}
	sp := g.prs[n]
	pr := PR{Number: n, Title: sp.Title, URL: sp.URL, HeadRefName: sp.HeadRefName,
		HeadRefOid: sp.HeadRefOid, IsDraft: sp.IsDraft, Repo: g.repo}
	pr.Author.Login = sp.Author.Login
	return pr
}

// visibleStackRows is the stack view: its lines, then its layers.
func (m model) visibleStackRows() []visibleRow {
	g := m.stackGraph()
	if g == nil {
		return nil
	}
	lines := stackLines(g, m.stack.at)
	if len(lines) == 0 {
		return nil
	}
	title := "Stack review"
	if len(lines) > 1 {
		title = fmt.Sprintf("Stack review · %d lines", len(lines))
	}
	out := []visibleRow{{sectionTitle: title, sectionStyle: styleSectionTodo}}
	for i := range lines {
		tip := m.layerPR(g, lines[i].tip().Number)
		out = append(out, visibleRow{pr: &tip, line: &lines[i]})
	}
	out = append(out, visibleRow{sectionTitle: "Layers", sectionStyle: styleSectionWait})
	for _, lp := range stackLayers(g, m.stack.at) {
		pr := m.layerPR(g, lp.n)
		// A layer of your own is the mine pane's kind of row: its
		// workspace is its branch's, and its keys are bindings.mine.
		mine := m.me != "" && pr.Author.Login == m.me
		out = append(out, visibleRow{pr: &pr, status: pr.MyReviewStatus(m.me), treePrefix: lp.prefix, mine: mine})
	}
	return out
}

// renderLineRow draws a line of the stack: the stack workspace's
// badges, the line from bottom to tip, and the tip's title.
func (m model) renderLineRow(row visibleRow, selected bool) string {
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	starting := ""
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	l := row.line
	span := fmt.Sprintf("#%d → #%d", l.PRs[0].Number, l.tip().Number)
	prefix := fmt.Sprintf("%s%s %-15s %s  %s  ",
		cursor,
		styleStack.Render("≡"),
		span,
		workspaceBadges(m.localOf(row), starting),
		styleDim.Render(fmt.Sprintf("%2d layers", len(l.PRs))),
	)
	w := m.prTitleWidth(lipgloss.Width(prefix))
	if w == 0 {
		w = 64
	}
	// The row's tip, not the line's: its title is the fetched one once
	// the layers land, where a recorded line knows only the list's.
	return prefix + trim(row.pr.Title, w)
}

// renderLayerRow draws one layer: its workspace, your review of it and
// its checks, then the title behind the tree's branches.
func (m model) renderLayerRow(row visibleRow, selected bool) string {
	cursor := "  "
	if selected {
		cursor = "▸ "
	}
	starting := ""
	if _, ok := m.inflight[row.id()]; ok {
		starting = m.spinner.View()
	}
	draft := ""
	if row.pr.IsDraft {
		draft = styleDraft.Render(" [draft]")
	}
	mark := " "
	if row.pr.Number == m.stack.at {
		mark = styleHeader.Render("•")
	}
	// The graph carries no age and the details may not have landed:
	// nothing is better than a question mark there.
	age := ""
	if row.pr.UpdatedAt != "" {
		age = relativeAge(row.pr.UpdatedAt)
	}
	author := ""
	if row.pr.Author.Login != "" {
		author = " (" + row.pr.Author.Login + ")"
	}
	prefix := fmt.Sprintf("%s#%-5d %s %s  %s %s %s",
		cursor,
		row.pr.Number,
		badges(m.localOf(row), starting, verdictOf(row, m.me)),
		checksGlyph(row.pr.CI),
		styleDim.Render(fmt.Sprintf("%3s", age)),
		mark,
		styleStack.Render(row.treePrefix),
	)
	w := m.prTitleWidth(lipgloss.Width(prefix))
	if w == 0 {
		w = max(64-lipgloss.Width(row.treePrefix), 24)
	}
	return strings.TrimRight(prefix+titleCell(row.pr.Title, draft, w)+author, " ")
}

// checksGlyph is the head commit's checks, as the mine pane draws
// them: green, failing, still running, or nothing reported.
func checksGlyph(ci string) string {
	switch ci {
	case "SUCCESS":
		return styleApproved.Render("✓")
	case "FAILURE", "ERROR":
		return styleChangesReqd.Render("✗")
	case "PENDING", "EXPECTED":
		return styleDraft.Render("◐")
	}
	return " "
}

// stackWorkspaceOf is the stack workspace of a line, by the records
// this clone keeps: the one whose tip is on the line. "" when the line
// has none yet.
func (m model) stackWorkspaceOf(l *stackLine) string {
	if r, ok := recordFor(m.stackRecords, l.Repo, *l); ok {
		return r.name
	}
	return ""
}

// bindingFits reports whether the i-th binding of the list's keys is
// one for the selected row. Only the stack view mixes them: its keys
// are the review pane's bindings then the mine pane's, and a layer of
// your own takes the second kind — f there is the review you were
// given, on someone else's layer the feedback you gave.
func (m model) bindingFits(i int) bool {
	if m.stack == nil {
		return true
	}
	row, ok := m.selectedRow()
	if !ok {
		return true
	}
	return (i >= len(m.cfg.Bindings.PR)) == row.mine
}

// stackCountsSummary is the stack view's action row.
func (m model) stackCountsSummary() string {
	g := m.stackGraph()
	if g == nil {
		return styleDim.Render("no stacks fetched yet")
	}
	l, err := g.line(m.stack.at)
	if err != nil {
		return styleDim.Render(fmt.Sprintf("#%d is no longer an open pull request", m.stack.at))
	}
	if m.stack.recorded != nil {
		return styleDim.Render(fmt.Sprintf("%d layers · %d lines · merged: the lines as their stack workspaces recorded them",
			len(stackLayers(g, m.stack.at)), len(stackLines(g, m.stack.at))))
	}
	return styleDim.Render(fmt.Sprintf("%d layers · %d lines · on %s",
		len(stackLayers(g, m.stack.at)), len(stackLines(g, m.stack.at)), l.Base))
}

// stackLegend explains the view's own row, in its help.
func (m model) stackViewLegend() string {
	return lipgloss.JoinVertical(lipgloss.Left,
		m.reviewLegend(),
		"",
		styleHeader.Render("Stack"),
		fmt.Sprintf("  %s   a line from the trunk to a tip: Enter opens its stack review, %s closes it", styleDim.Render("≡"), m.keys.Cleanup.Help().Key),
		fmt.Sprintf("  %s   a layer's checks: green, failing, still running", styleApproved.Render("✓")+styleChangesReqd.Render("✗")+styleDraft.Render("◐")),
		fmt.Sprintf("  %s   the pull request the view was opened on", styleHeader.Render("•")),
	)
}

// enterStack opens the stack view on the selected row, or says why
// not.
func (m model) enterStack() (model, tea.Cmd) {
	pr := m.selectedPR()
	if pr == nil {
		return m, nil
	}
	view := &stackView{at: pr.Number, back: m.cursor, filter: m.search.Value()}
	if pr.MergedAt != "" {
		// No open stack holds a merged layer; a workspace that reviewed
		// its line may, and its row's badges say so.
		if view.recorded = m.recordedGraph(pr); view.recorded == nil {
			m.notice = fmt.Errorf("#%d has merged, and no stack workspace holds it", pr.Number)
			return m, nil
		}
		view.repo = view.recorded.repo
	} else {
		g := m.graphOf(pr)
		if g == nil {
			m.notice = fmt.Errorf("#%d: the stacks have not been fetched yet", pr.Number)
			return m, nil
		}
		if l, err := g.line(pr.Number); err != nil || !l.stacked() {
			m.notice = fmt.Errorf("#%d is not a layer of a stack", pr.Number)
			return m, nil
		}
		view.repo = g.repo
	}
	m.stack = view
	m.search.SetValue("")
	m.search.Blur()
	m.keys = newKeyMap(m.cfg.Keys, m.bindings()) // both panes' bindings: a layer is of either kind
	m.labelKeysForPane()
	m.cursor = 0
	m.clampCursor()
	m.refreshList()
	return m, tea.Batch(m.fetchStackLayers, m.fetchLocal)
}

// leaveStack goes back to the list, on the row the view was opened
// from.
func (m model) leaveStack() (model, tea.Cmd) {
	m.cursor = m.stack.back
	m.search.SetValue(m.stack.filter)
	m.stack = nil
	m.keys = newKeyMap(m.cfg.Keys, m.bindings())
	m.labelKeysForPane()
	m.clampCursor()
	m.refreshList()
	return m, m.fetchLocal
}

// fetchStackLayers reads the details of the view's layers.
func (m model) fetchStackLayers() tea.Msg {
	gen := m.fetchGen
	g := m.stackGraph()
	if g == nil {
		return stackLayersMsg{gen: gen}
	}
	var nums []int
	for _, lp := range stackLayers(g, m.stack.at) {
		nums = append(nums, lp.n)
	}
	prs, err := layerDetails(g.repo, nums)
	if err != nil {
		return stackLayersMsg{gen: gen, repo: g.repo, at: m.stack.at, err: err}
	}
	return stackLayersMsg{gen: gen, repo: g.repo, at: m.stack.at, prs: prs}
}

// layerFields is what a layer row shows beyond the graph: your review
// of it, as the review list reads one, and its head commit's checks.
const layerFields = `number title url headRefName headRefOid updatedAt isDraft author { login }
  reviews(last: 100) { nodes { author { login } state submittedAt commit { oid } } }
  commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

// layerDetails reads the pull requests nums of slug in one query, one
// alias per pull request.
func layerDetails(slug string, nums []int) ([]PR, error) {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok || len(nums) == 0 {
		return nil, nil
	}
	var q strings.Builder
	q.WriteString("query($owner: String!, $name: String!) { repository(owner: $owner, name: $name) {")
	for _, n := range nums {
		fmt.Fprintf(&q, " pr%d: pullRequest(number: %d) { %s }", n, n, layerFields)
	}
	q.WriteString(" } }")
	out, err := runOut(exec.Command("gh", "api", "graphql",
		"-f", "query="+q.String(), "-f", "owner="+owner, "-f", "name="+name))
	if err != nil {
		return nil, err
	}
	var resp struct {
		Data struct {
			Repository map[string]*struct {
				Number      int    `json:"number"`
				Title       string `json:"title"`
				URL         string `json:"url"`
				HeadRefName string `json:"headRefName"`
				HeadRefOid  string `json:"headRefOid"`
				UpdatedAt   string `json:"updatedAt"`
				IsDraft     bool   `json:"isDraft"`
				Author      struct {
					Login string `json:"login"`
				} `json:"author"`
				Reviews struct {
					Nodes []Review `json:"nodes"`
				} `json:"reviews"`
				Commits struct {
					Nodes []struct {
						Commit struct {
							StatusCheckRollup struct {
								State string `json:"state"`
							} `json:"statusCheckRollup"`
						} `json:"commit"`
					} `json:"nodes"`
				} `json:"commits"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return nil, fmt.Errorf("parse graphql: %w", err)
	}
	var prs []PR
	for _, n := range nums {
		node := resp.Data.Repository["pr"+strconv.Itoa(n)]
		if node == nil {
			continue
		}
		pr := PR{Number: node.Number, Title: node.Title, URL: node.URL, HeadRefName: node.HeadRefName,
			HeadRefOid: node.HeadRefOid, UpdatedAt: node.UpdatedAt, IsDraft: node.IsDraft,
			Reviews: node.Reviews.Nodes, Repo: slug}
		pr.Author.Login = node.Author.Login
		if c := node.Commits.Nodes; len(c) > 0 {
			pr.CI = c[0].Commit.StatusCheckRollup.State
		}
		prs = append(prs, pr)
	}
	return prs, nil
}
