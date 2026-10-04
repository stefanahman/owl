package main

import (
	"charm.land/lipgloss/v2"
)

// ------------------------------------------------------------
// Styles
// ------------------------------------------------------------

// Claude-state styles follow the agent-state vocabulary (working /
// blocked / done / idle) the multiplexer reports. Their colours come
// from the `theme` config (applyTheme); everything else is fixed.
var (
	styleClaudeWorking lipgloss.Style // ©  Claude actively processing
	styleClaudeBlocked lipgloss.Style // ©  Claude waiting on you (permission, question, plan)
	styleClaudeDone    lipgloss.Style // ©  Claude finished (done + `*`, or idle without)

	styleWorktree      = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // blue    ⎇  worktree present
	styleClaudeNeutral = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // gray    ©  session exists, no state (fresh window)
	styleApproved      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green   ✓  I approved (current verdict)
	styleReviewStale   = lipgloss.NewStyle().Foreground(lipgloss.Color("214")) // amber   ✓ or ·  my review predates the head
	styleChangesReqd   = lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // orange  ⚠  any reviewer requested changes
	styleDim           = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleHeader        = lipgloss.NewStyle().Bold(true)
	styleSectionTodo   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("214")) // amber
	styleSectionWait   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))  // blue
	styleSectionOK     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))  // green
	styleSectionMerged = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("141")) // purple (GitHub's merged color)
	// Cancelled recedes: it is on the list so you notice it happened,
	// not so it competes with the work that is still alive.
	styleSectionCancelled = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("244"))
	styleDraft            = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // dim for [draft]
	styleSearchLabel      = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
)

func applyTheme(t ThemeConfig) {
	styleClaudeWorking = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Working))
	styleClaudeBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Blocked))
	styleClaudeDone = lipgloss.NewStyle().Foreground(lipgloss.Color(t.Done))
}
