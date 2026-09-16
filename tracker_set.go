// A Tracker over several Linear workspaces.
//
// Linear keeps workspaces apart on purpose: separate accounts, separate
// keys, and no query that reaches across. That is right for a company
// and a person who are legally two things, and wrong for the one desk
// they are both worked from. owl is where they meet, so the joining
// happens here — the lists merge, and everything that names one issue
// or one project is routed back to the workspace it came from.
//
// The set is the same Tracker the rest of owl already takes, so no
// list, no open and no close knows there is more than one workspace.
package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// workspaceTracker is one workspace: what to call it, the team keys it
// claims, and the client that reads it.
type workspaceTracker struct {
	name string
	// teams are the keys a branch may carry that belong here; team is
	// the one new issues would be filed in, which is at most one and is
	// often unset. Kept beside the tracker rather than read back out of
	// it: the set composes Trackers, and a set that has to know which
	// implementation it holds is not composing them.
	teams   []string
	team    string
	tracker Tracker
}

// trackerSet reads several workspaces as one.
//
// Partial answers are the point. A key that has expired on one
// workspace is a Tuesday, and blanking a list that has good data in it
// because of that helps nobody: what answered is shown, what failed is
// said through notify, and only a set where every workspace failed
// returns an error.
type trackerSet struct {
	workspaces []workspaceTracker
	// notify says what could not be read, where the multiplexer shows
	// messages. nil for none, as elsewhere.
	notify func(string)
}

// newTrackerSet builds a tracker for every configured workspace.
func newTrackerSet(cfg Config, notify func(string)) Tracker {
	var set trackerSet
	set.notify = notify
	for i, ws := range cfg.Linear {
		set.workspaces = append(set.workspaces, workspaceTracker{
			name:  ws.Name,
			teams: ws.TeamKeys(),
			team:  ws.Team,
			tracker: Linear{
				Token: secret{name: cfg.Linear.cacheName(i), ref: ws.Token, account: ws.Account, notify: notify},
				Team:  ws.Team,
			},
		})
	}
	return set
}

// report tells the user a workspace could not be read, and keeps the
// error for the case where none of them could.
func (s trackerSet) report(errs []error, w workspaceTracker, err error) []error {
	if s.notify != nil {
		s.notify(fmt.Sprintf("linear: %s: %v", w.name, err))
	}
	return append(errs, fmt.Errorf("%s: %w", w.name, err))
}

// gather runs read over every workspace and concatenates what comes
// back, stamping each item with the workspace it came from. It fails
// only when every workspace did.
func gather[T any](s trackerSet, read func(Tracker) ([]T, error), stamp func(*T, string)) ([]T, error) {
	var all []T
	var errs []error
	for _, w := range s.workspaces {
		items, err := read(w.tracker)
		if err != nil {
			errs = s.report(errs, w, err)
			continue
		}
		for i := range items {
			stamp(&items[i], w.name)
		}
		all = append(all, items...)
	}
	if len(errs) == len(s.workspaces) && len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return all, nil
}

// byUpdatedAt restores the order every list promises — newest change
// first — which concatenating two sorted answers does not keep.
func byUpdatedAt[T any](items []T, at func(T) time.Time) []T {
	sort.SliceStable(items, func(i, j int) bool { return at(items[i]).After(at(items[j])) })
	return items
}

func stampIssue(i *Issue, name string)     { i.Workspace = name }
func stampProject(p *Project, name string) { p.Workspace = name }

func issueUpdatedAt(i Issue) time.Time     { return i.UpdatedAt }
func projectUpdatedAt(p Project) time.Time { return p.UpdatedAt }

func (s trackerSet) Issues() ([]Issue, error) {
	out, err := gather(s, Tracker.Issues, stampIssue)
	return byUpdatedAt(out, issueUpdatedAt), err
}

func (s trackerSet) Done(since time.Time) ([]Issue, error) {
	out, err := gather(s, func(t Tracker) ([]Issue, error) { return t.Done(since) }, stampIssue)
	return byUpdatedAt(out, issueUpdatedAt), err
}

func (s trackerSet) Cancelled(since time.Time) ([]Issue, error) {
	out, err := gather(s, func(t Tracker) ([]Issue, error) { return t.Cancelled(since) }, stampIssue)
	return byUpdatedAt(out, issueUpdatedAt), err
}

func (s trackerSet) Projects() ([]Project, error) {
	out, err := gather(s, Tracker.Projects, stampProject)
	return byUpdatedAt(out, projectUpdatedAt), err
}

func (s trackerSet) DoneProjects(since time.Time) ([]Project, error) {
	out, err := gather(s, func(t Tracker) ([]Project, error) { return t.DoneProjects(since) }, stampProject)
	return byUpdatedAt(out, projectUpdatedAt), err
}

// forKey is the workspace whose team claims the key, and whether one
// does. DEV-12 is the DEV team's, and config has already refused a
// second workspace claiming DEV.
func (s trackerSet) forKey(key string) (workspaceTracker, bool) {
	team := key
	if i := strings.LastIndexByte(key, '-'); i > 0 {
		team = key[:i]
	}
	for _, w := range s.workspaces {
		for _, t := range w.teams {
			if strings.EqualFold(t, team) {
				return w, true
			}
		}
	}
	return workspaceTracker{}, false
}

// Issue goes to the workspace whose team the key names. An unclaimed
// team is asked of every workspace rather than refused: a key can be
// real and its team simply left out of `teams`, and a wrong answer
// here is a workspace owl says does not exist.
func (s trackerSet) Issue(key string) (Issue, error) {
	if w, ok := s.forKey(key); ok {
		issue, err := w.tracker.Issue(key)
		issue.Workspace = w.name
		return issue, err
	}
	return tryEach(s, key, Tracker.Issue, func(i *Issue, n string) { i.Workspace = n }, func(i Issue) bool { return i.ID != "" })
}

// Project and ProjectIssues are asked of each workspace in turn: a
// project is a UUID or a slug, and neither says where it lives. One
// wasted call of ninety milliseconds is the cost, and only until the
// project list has been read once — a row there knows its workspace.
func (s trackerSet) Project(id string) (Project, error) {
	return tryEach(s, id, Tracker.Project, func(p *Project, n string) { p.Workspace = n }, func(p Project) bool { return p.ID != "" })
}

func (s trackerSet) ProjectIssues(id string) ([]Issue, error) {
	out, err := tryEach(s, id, Tracker.ProjectIssues, func(is *[]Issue, n string) {
		for i := range *is {
			(*is)[i].Workspace = n
		}
	}, func(is []Issue) bool { return len(is) > 0 })
	return out, err
}

// tryEach asks every workspace for one thing and takes the first real
// answer. Linear answers a project of another workspace with an empty
// value and no error, so found says what counts as an answer.
func tryEach[T any](s trackerSet, id string, read func(Tracker, string) (T, error), stamp func(*T, string), found func(T) bool) (T, error) {
	var zero T
	var errs []error
	for _, w := range s.workspaces {
		got, err := read(w.tracker, id)
		if err != nil {
			errs = s.report(errs, w, err)
			continue
		}
		if found(got) {
			stamp(&got, w.name)
			return got, nil
		}
	}
	if len(errs) > 0 {
		return zero, errors.Join(errs...)
	}
	return zero, nil
}

// Create files in the one workspace that says where new issues go.
// owl writes in exactly one place — `owl hoot` — and which of several
// workspaces it should write to is a question the config answers by
// naming a team, or does not answer at all.
func (s trackerSet) Create(title string) (Issue, error) {
	var targets []workspaceTracker
	for _, w := range s.workspaces {
		if w.team != "" {
			targets = append(targets, w)
		}
	}
	switch len(targets) {
	case 0:
		return Issue{}, errors.New("linear: no team configured (linear.team, the key in BAR-123)")
	case 1:
		issue, err := targets[0].tracker.Create(title)
		issue.Workspace = targets[0].name
		return issue, err
	default:
		var names []string
		for _, w := range targets {
			names = append(names, w.name)
		}
		return Issue{}, fmt.Errorf("linear: %s each name a team for new issues; only one workspace can be the one owl files into", strings.Join(names, " and "))
	}
}
