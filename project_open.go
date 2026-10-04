// `owl project open|start|close <id>`: the conversation that sits
// above the issues. A worktree detached at the remote's default
// branch, a window in the projects container, and one Claude session
// per project — owl gives it its id, so it resumes as itself rather
// than as whatever `-c` finds.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// projectState is what owl keeps per project between sessions. It is
// machine-local, like the Linear token: a Claude session lives in
// ~/.claude on this machine and means nothing on another, so it does
// not belong in the repo.
type projectState struct {
	Session string `json:"session"` // the uuid owl passes to --session-id
	// Name is the project's name at its first open. It is never updated:
	// in a state from before Workspace was recorded, it is what the
	// workspace's name comes from.
	Name string `json:"name"`
	// Workspace is the name the project's workspace goes by — its
	// worktree's, its window's — recorded when owl creates or finds it,
	// so a rename in Linear does not move it.
	Workspace string `json:"workspace,omitempty"`
}

// projectStatePath is $XDG_STATE_HOME/owl/projects.json, else
// ~/.local/state/owl/projects.json.
func projectStatePath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "owl", "projects.json"), nil
}

// loadProjectStates reads the file, keyed by Linear's project id. A
// missing or unreadable file is an empty map: the conversation starts
// fresh, which is right when there is nothing to resume.
func loadProjectStates() map[string]projectState {
	states := map[string]projectState{}
	path, err := projectStatePath()
	if err != nil {
		return states
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return states
	}
	_ = json.Unmarshal(b, &states)
	return states
}

// saveProjectState records one project's state. The file is every
// project's, so the read, the change and the write happen under a lock
// of their own, and the write lands whole by a rename: two projects
// opened at once each keep their entry, and a reader never sees half a
// file — which reads as empty, and would lose every session id.
func saveProjectState(id string, st projectState) error {
	path, err := projectStatePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", lock.Name(), err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	states := loadProjectStates()
	states[id] = st
	b, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".projects-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// newSessionID is a v4 UUID: what `claude --session-id` takes.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	s := hex.EncodeToString(b[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

// findProject resolves what the user typed to one project: Linear's
// slug id, the slug in its workspace's name (the one recorded, or the
// one its current name gives), else a case-insensitive fragment of the
// name. Several matches is an error naming them — guessing between
// projects would open the wrong conversation.
func findProject(tracker Tracker, id string) (Project, error) {
	// An id resolves on its own, and that is the path the list uses:
	// one lookup instead of every project the user works in, with all
	// their milestones, behind a filter that scans the workspace's
	// issues. The difference is 90ms against seconds.
	if p, err := tracker.Project(id); err == nil && p.ID != "" {
		return p, nil
	}
	all, err := tracker.Projects()
	if err != nil {
		return Project{}, err
	}
	// The workspace's slug resolves as well as the current name's: it
	// is what the window list shows after a rename.
	states := loadProjectStates()
	var byName []Project
	for _, p := range all {
		if p.SlugID == id || slices.Contains(projectNames(p, states[p.ID], takenNames(states, p.ID)), "proj-"+id) {
			return p, nil
		}
		if strings.Contains(strings.ToLower(p.Name), strings.ToLower(id)) {
			byName = append(byName, p)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Project{}, fmt.Errorf("no project matches %q", id)
	}
	names := make([]string, 0, len(byName))
	for _, p := range byName {
		names = append(names, p.Name)
	}
	return Project{}, fmt.Errorf("%q matches %d projects: %s", id, len(byName), strings.Join(names, "; "))
}

func runProjectOpen(cfg Config, tracker Tracker, args []string, out io.Writer, arrive bool) error {
	id, prompt, base, err := parseOpenArgs("project", "project", args)
	if err != nil {
		return err
	}
	if base != "" {
		return usageError("project open: --base is the issue side's; a project's conversation has no branch")
	}
	p, err := findProject(tracker, id)
	if err != nil {
		return err
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	// The lock is the workspace's, named from the state as it is now;
	// the state is read again once it is held. A second open of a
	// project never opened, waiting here while the first saves its
	// session id, would otherwise mint a second id over it — and the
	// transcript on disk is the first one's.
	before := loadProjectStates()
	names := projectNames(p, before[p.ID], takenNames(before, p.ID))
	unlock, err := lockWorkspace(repo, names[0])
	if err != nil {
		return err
	}
	defer unlock()
	states := loadProjectStates()
	st := states[p.ID]
	names = projectNames(p, st, takenNames(states, p.ID))
	wt, name, err := ensureProjectWorktree(cfg, repo, names, st.Session, out)
	if err != nil {
		return err
	}

	// The session id is owl's, not Claude's to pick: a project resumes
	// as the same conversation every time, and the forks below it will
	// resume from a known parent. Where its workspace is goes beside it.
	if st.Session == "" || st.Workspace != name {
		if st.Session == "" {
			session, err := newSessionID()
			if err != nil {
				return err
			}
			st = projectState{Session: session, Name: p.Name}
		}
		st.Workspace = name
		if err := saveProjectState(p.ID, st); err != nil {
			return err
		}
	}

	ws := workspace{
		label:   p.Name,
		name:    name,
		dir:     wt,
		first:   strings.ReplaceAll(cfg.Project.Prompt, "{name}", p.Name),
		session: st.Session,
		// The conversation is named for the project as Linear has it
		// now, on every open: the workspace keeps the name it was made
		// under, so after a rename this is where the current one shows —
		// Claude's prompt box, its /resume picker and the terminal
		// title, which a multiplexer's tab and cmux-describe read. On a
		// resume it adds a newer title, which is the one Claude shows.
		args: []string{"--name", shellQuote(p.Name)},
		env: map[string]string{
			"OWL_PROJECT":         p.Name,
			"OWL_PROJECT_SLUG":    p.SlugID,
			"OWL_PROJECT_SESSION": st.Session,
		},
	}
	return ws.open(cfg, newWindows(cfg, projects), repo, prompt, arrive, out)
}

// ensureProjectWorktree returns the project's worktree and the name it
// goes by: the first of names a worktree already carries, in order, or
// else one created detached at the remote's default branch — where
// Claude holds the project's conversation (its session's transcript),
// under one of names or under any proj- name a project renamed more
// than once last used, and under names[0] when there is none.
//
// Detached, because the default branch is checked out in the main
// worktree already and git refuses the same branch twice — and because
// a project's agent reads, plans and dispatches. The work goes on the
// issues' branches; this one has nothing to commit.
func ensureProjectWorktree(cfg Config, repo string, names []string, session string, out io.Writer) (string, string, error) {
	found := sessionWorkspace(repo, cfg.WorktreesDir, session)
	look := names
	if found != "" && !slices.Contains(names, found) {
		look = append(slices.Clone(names), found)
	}
	if list, err := listWorktrees(repo); err == nil {
		for _, name := range look {
			for _, w := range list {
				if !w.Prunable && filepath.Base(w.Path) == name {
					return w.Path, name, nil
				}
			}
		}
	}
	if _, err := git(repo, "fetch", "--quiet", cfg.Remote); err != nil {
		return "", "", err
	}
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return "", "", err
	}
	_, _ = git(repo, "worktree", "prune")
	name := names[0]
	if i := slices.IndexFunc(names, func(n string) bool { return hasSession(filepath.Join(repo, cfg.WorktreesDir, n), session) }); i >= 0 {
		name = names[i]
	} else if found != "" {
		name = found
	}
	wt := filepath.Join(repo, cfg.WorktreesDir, name)
	base := defaultBranch(repo, cfg.Remote)
	fmt.Fprintf(out, "creating %s detached at %s/%s\n", wt, cfg.Remote, base)
	if _, err := git(repo, "worktree", "add", "--detach", wt, cfg.Remote+"/"+base); err != nil {
		return "", "", err
	}
	return wt, name, nil
}

// runProjectClose removes a project's worktree and window. Its
// conversation survives on disk, and its session id in the state file,
// so a later open resumes both.
func runProjectClose(cfg Config, tracker Tracker, args []string, out io.Writer) error {
	force, rest := closeFlags(args)
	if len(rest) != 1 {
		return usageError("project close: expected one project")
	}
	p, err := findProject(tracker, rest[0])
	if err != nil {
		return err
	}
	// One workspace, the one open would use: the first of its names a
	// worktree carries, else the first a window does. Matching either
	// name would let the window come from one workspace and the worktree
	// from the other, where a project has both. The lock is open's.
	states := loadProjectStates()
	names := projectNames(p, states[p.ID], takenNames(states, p.ID))
	mx := newWindows(cfg, projects)
	name := names[0]
	if repo, err := mainRepo("."); err == nil {
		name = pickProjectWorkspace(repo, cfg.WorktreesDir, names, mx.Windows())
	}
	isIt := func(n string) bool { return n == name }
	return closeWorkspace(cfg, mx, names[0], isIt, force, out)
}

// pickProjectWorkspace is the first of names a worktree under
// worktreesDir carries, else the first a window does, else names[0].
func pickProjectWorkspace(repo, worktreesDir string, names, windows []string) string {
	if list, err := listWorktrees(repo); err == nil {
		dir := filepath.Join(repo, worktreesDir)
		for _, n := range names {
			for _, w := range list {
				if !w.Prunable && filepath.Dir(w.Path) == dir && filepath.Base(w.Path) == n {
					return n
				}
			}
		}
	}
	for _, n := range names {
		if slices.Contains(windows, n) {
			return n
		}
	}
	return names[0]
}
