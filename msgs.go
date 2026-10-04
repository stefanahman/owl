package main

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stefanahman/mux"
)

// ------------------------------------------------------------
// Messages
// ------------------------------------------------------------

// prsMsg and mergedMsg carry the round (model.fetchGen) they were
// started in; a result from an older round is ignored, so a slow
// fetch can't overwrite a newer one.
type prsMsg struct {
	gen int
	prs []PR
	// me is the authenticated login, which the same query answers: the
	// PR rows need it to say which verdict is yours, and a second `gh
	// api user` for one string is a whole round trip on every refresh.
	me string
}
type mergedMsg struct {
	gen int
	prs []PR
}

// issuesMsg and issuePRsMsg are the issue list's fetches: the open
// issues assigned to the user, and the repo's open PRs, which the rows
// show against the issue key their head branch carries.
type issuesMsg struct {
	gen    int
	issues []Issue
}
type issuePRsMsg struct {
	gen int
	prs []PR
}

// doneMsg carries the issues completed inside doneWindow — the issue
// list's answer to mergedMsg.
type doneMsg struct {
	gen    int
	issues []Issue
}

// watchMsg carries the driver the list will read state through and the
// channel that says when to. Both are nil where the multiplexer has no
// watch, and the timer alone then does the work it always did.
type watchMsg struct {
	driver mux.Driver
	signal <-chan struct{}
}

// statesMsg carries the agent states alone — every window the list's
// scopes own, and what the agent in each is doing. A watch signal
// answers this and leaves the worktrees alone.
type statesMsg map[string]string

// stateChangedMsg is one signal from the multiplexer: a later States()
// would answer differently. open reports whether the watch is still
// live — a closed channel must not be waited on again.
type stateChangedMsg struct{ open bool }

// cancelledMsg carries the issues cancelled inside cancelledWindow.
type cancelledMsg struct {
	gen    int
	issues []Issue
}

// drillIssuesMsg carries the issues of the project being drilled into.
type drillIssuesMsg struct {
	gen    int
	issues []Issue
}

// projectsMsg is the project list's fetch: the open projects the user
// works in.
type projectsMsg struct {
	gen      int
	projects []Project
}

// mineMsg carries your own PRs: the open ones the mine pane groups by
// what is blocking them, and the ones that landed inside
// merged_window, which no other list shows — the review list excludes
// your own by author, and an open-PR fetch loses a PR the moment it
// merges.
type mineMsg struct {
	gen    int
	prs    []PR
	merged []PR
}

// doneProjectsMsg carries the projects completed inside
// doneProjectWindow — the project list's answer to doneMsg.
type doneProjectsMsg struct {
	gen      int
	projects []Project
}

// localTickMsg asks for the whole local overlay again: the worktrees
// and the agent states both.
//
// Under a watch it is the safety net rather than the mechanism: what
// it catches then is what a watch cannot say — a worktree made outside
// owl, a subscription that died quietly. Without one it is still the
// mechanism, and still runs at the old cadence.
type localTickMsg struct{}

// Two cadences, because the timer is two different things depending on
// whether a watch is live. Under tmux and herdr there is none, the
// timer is still the only way a © ever changes colour, and two seconds
// is what that costs there: one `tmux list-windows`, a few
// milliseconds. Under cmux a watch reports the change itself, and the
// timer drops to the slow one — polling every two seconds there was a
// process per workspace per tick.
const (
	localRefreshEvery   = 2 * time.Second
	localRefreshWatched = 30 * time.Second
)

// localRefreshInterval is how long the timer waits, given whether a
// watch is live.
func localRefreshInterval(watching bool) time.Duration {
	if watching {
		return localRefreshWatched
	}
	return localRefreshEvery
}

func localTick(watching bool) tea.Cmd {
	return tea.Tick(localRefreshInterval(watching), func(time.Time) tea.Msg { return localTickMsg{} })
}

// startWatch subscribes to the multiplexer's state changes, and keeps
// the driver it subscribed on: mux.Watch hangs the subscription off
// that instance, and States() then answers from it without running
// anything. A multiplexer with no watch answers a nil channel, and
// waitForState makes no command for one, so nothing waits on it.
func startWatch(ctx context.Context, cfg Config) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			return watchMsg{} // no list to outlive: nothing to watch for
		}
		d := stateDriver(cfg)
		if d == nil {
			return watchMsg{}
		}
		ch, err := mux.Watch(d, ctx)
		if err != nil || ch == nil {
			// No watch to be had — cmux unreachable, or already watched.
			// The driver is dropped with it, and deliberately: reads
			// through a kept one answer from its snapshot, and only a
			// mutating call ever clears that. Without a watch to keep it
			// current, a kept driver would report the states it saw first
			// for as long as the list ran. A fresh driver per read is
			// what the timer had before, and it is still correct.
			return watchMsg{}
		}
		return watchMsg{driver: d, signal: ch}
	}
}

// waitForState blocks on the watch until it signals, once. Re-armed on
// every signal, which is how a channel is read inside an Update loop.
func waitForState(ch <-chan struct{}) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		_, open := <-ch
		return stateChangedMsg{open: open}
	}
}

// errMsg is a failed fetch: the list is stale (or, with nothing to
// show yet, absent) until a fetch succeeds.
type errMsg struct {
	gen int
	err error
}

func (e errMsg) Error() string { return e.err.Error() }

// noticeMsg is the outcome of a user action — opening a URL, copying,
// switching client, open, close — shown in the action row until the
// next key press. It never replaces the list.
type noticeMsg struct{ err error }

// openedMsg / closedMsg report the end of an `open` / `close` child
// for a workspace — a PR's number or an issue's key — nil, or its
// failure with the child's stderr.
type openedMsg struct {
	id  string
	err error
}
type closedMsg struct {
	id  string
	err error
}
