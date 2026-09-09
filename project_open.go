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
	"strings"
)

// projectState is what owl keeps per project between sessions. It is
// machine-local, like the Linear token: a Claude session lives in
// ~/.claude on this machine and means nothing on another, so it does
// not belong in the repo.
type projectState struct {
	Session string `json:"session"` // the uuid owl passes to --session-id
	Name    string `json:"name"`    // the project's name, for messages
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

// saveProjectState records one project's session id.
func saveProjectState(id string, st projectState) error {
	path, err := projectStatePath()
	if err != nil {
		return err
	}
	states := loadProjectStates()
	states[id] = st
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(states, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
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
// slug id, else a case-insensitive fragment of the name. Several
// matches is an error naming them — guessing between projects would
// open the wrong conversation.
func findProject(tracker Tracker, id string) (Project, error) {
	all, err := tracker.Projects()
	if err != nil {
		return Project{}, err
	}
	var byName []Project
	for _, p := range all {
		if p.SlugID == id || projectSlug(p.Name) == id {
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
	id, prompt, err := parseOpenArgs("project", "project", args)
	if err != nil {
		return err
	}
	p, err := findProject(tracker, id)
	if err != nil {
		return err
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	name := "proj-" + projectSlug(p.Name)
	unlock, err := lockWorkspace(repo, name)
	if err != nil {
		return err
	}
	defer unlock()
	wt, err := ensureProjectWorktree(cfg, repo, name, out)
	if err != nil {
		return err
	}

	// The session id is owl's, not Claude's to pick: a project resumes
	// as the same conversation every time, and the forks below it will
	// resume from a known parent.
	states := loadProjectStates()
	st := states[p.ID]
	if st.Session == "" {
		session, err := newSessionID()
		if err != nil {
			return err
		}
		st = projectState{Session: session, Name: p.Name}
		if err := saveProjectState(p.ID, st); err != nil {
			return err
		}
	}
	// --resume only when the conversation is actually on disk. The id
	// alone proves nothing: the worktree may have been closed before
	// Claude ever wrote a session, and resuming one that does not exist
	// fails where starting it with the same id succeeds.
	agentArgs := []string{"--session-id", st.Session}
	if hasConversationFor(wt) {
		agentArgs = []string{"--resume", st.Session}
	}

	ws := workspace{
		label: p.Name,
		name:  name,
		dir:   wt,
		first: strings.ReplaceAll(cfg.Project.Prompt, "{name}", p.Name),
		args:  agentArgs,
		env: map[string]string{
			"OWL_PROJECT":         p.Name,
			"OWL_PROJECT_SLUG":    p.SlugID,
			"OWL_PROJECT_SESSION": st.Session,
		},
	}
	return ws.open(cfg, newWindows(cfg, projects), repo, prompt, arrive, out)
}

// ensureProjectWorktree returns the project's worktree, creating it
// detached at the remote's default branch when it is not there.
//
// Detached, because the default branch is checked out in the main
// worktree already and git refuses the same branch twice — and because
// a project's agent reads, plans and dispatches. The work goes on the
// issues' branches; this one has nothing to commit.
func ensureProjectWorktree(cfg Config, repo, name string, out io.Writer) (string, error) {
	if list, err := listWorktrees(repo); err == nil {
		for _, w := range list {
			if !w.Prunable && filepath.Base(w.Path) == name {
				return w.Path, nil
			}
		}
	}
	if _, err := git(repo, "fetch", "--quiet", cfg.Remote); err != nil {
		return "", err
	}
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return "", err
	}
	_, _ = git(repo, "worktree", "prune")
	wt := filepath.Join(repo, cfg.WorktreesDir, name)
	base := defaultBranch(repo, cfg.Remote)
	fmt.Fprintf(out, "creating %s detached at %s/%s\n", wt, cfg.Remote, base)
	if _, err := git(repo, "worktree", "add", "--detach", wt, cfg.Remote+"/"+base); err != nil {
		return "", err
	}
	return wt, nil
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
	name := "proj-" + projectSlug(p.Name)
	isIt := func(n string) bool { return n == name }
	return closeWorkspace(cfg, newWindows(cfg, projects), name, isIt, force, out)
}
