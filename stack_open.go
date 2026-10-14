// `owl pr open <N> --stack`: one workspace for the line through N,
// detached at the line's tip, whose tree holds every layer. Each layer
// can still be opened on its own, beside it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// stackRecord is what owl keeps about a stack workspace, in the
// clone's git directory beside the workspace locks. A stack's name
// carries no PR number to find it by, so this is how it is found
// again: by its tip, which every open moves to the line's current one.
type stackRecord struct {
	name string // the workspace: the file is <name>.json
	Repo string `json:"repo"` // owner/name, the repository the PRs are in
	Tip  int    `json:"tip"`
	PRs  []int  `json:"prs"` // the line at the last open, bottom first
}

func stackRecordsDir(repo string) (string, error) {
	common, err := gitCommonDir(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(common, "owl", "stacks"), nil
}

// readStackRecords is every stack workspace this clone has opened,
// closed ones included: a closed stack keeps its record, as a closed
// review keeps its conversation, so opening it again finds its name.
func readStackRecords(repo string) ([]stackRecord, error) {
	dir, err := stackRecordsDir(repo)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []stackRecord
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || e.IsDir() || !isStackName(name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		r := stackRecord{name: name}
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Join(dir, e.Name()), err)
		}
		out = append(out, r)
	}
	return out, nil
}

// writeStackRecord replaces the record whole, through a rename, so a
// reader never sees half of one.
func writeStackRecord(repo string, r stackRecord) error {
	dir, err := stackRecordsDir(repo)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, r.name+".json.tmp")
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, r.name+".json"))
}

// recordFor is the record of the workspace for line l: one whose tip
// is on the line. A layer pushed on top of the tip extends the line
// and keeps its workspace; a fork below the tip starts a line that
// does not pass through it, which gets a workspace of its own. Two
// records on one line are two opens that each saw a shorter line, and
// the one whose tip is higher is the later.
func recordFor(records []stackRecord, slug string, l stackLine) (stackRecord, bool) {
	at := -1
	var found stackRecord
	for _, r := range records {
		// Either spelling: a record holds the remote URL's, the list
		// GitHub's own nameWithOwner.
		if !strings.EqualFold(r.Repo, slug) {
			continue
		}
		if i := slices.Index(l.numbers(), r.Tip); i > at {
			at, found = i, r
		}
	}
	return found, at >= 0
}

// stackName is a new line's workspace name: `stack-` and the slug of
// its tip's title, which is what tells two lines on one bottom apart,
// or the tip's number where the slug is empty or already somebody's.
func stackName(tip stackPR, taken map[string]bool) string {
	if s := slugify(tip.Title); s != "" && !taken["stack-"+s] {
		return "stack-" + s
	}
	return "stack-" + strconv.Itoa(tip.Number)
}

// takenStackNames are the names a new stack workspace must not take:
// every record's, and every stack worktree's, recorded or not.
func takenStackNames(repo string, records []stackRecord) map[string]bool {
	taken := map[string]bool{}
	for _, r := range records {
		taken[r.name] = true
	}
	if list, err := listWorktrees(repo); err == nil {
		for _, wt := range list {
			if h := wt.handle(); isStackName(h) {
				taken[h] = true
			}
		}
	}
	return taken
}

// forkError is `--stack` on a pull request below a fork: the line it
// would review is not one line.
type forkError struct {
	at   int
	fork int
	tips []stackPR
}

func (e forkError) Error() string {
	var b strings.Builder
	where := fmt.Sprintf("#%d forks", e.fork)
	if e.fork != e.at {
		where = fmt.Sprintf("the stack forks above #%d, at #%d", e.at, e.fork)
	}
	fmt.Fprintf(&b, "%s into %d lines: open the one to review by its tip\n", where, len(e.tips))
	for _, t := range e.tips {
		fmt.Fprintf(&b, "  owl pr open %d --stack   %s\n", t.Number, t.Title)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// runOpenStack opens the workspace for the line through PR n.
func runOpenStack(cfg Config, n int, prompt string, out io.Writer, arrive bool) error {
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	slug := currentRepo(cfg.Remote)
	if slug == "" {
		return fmt.Errorf("no GitHub repo: the working directory has no %q remote", cfg.Remote)
	}
	g, err := repoGraph(repo, slug)
	if err != nil {
		return err
	}
	l, err := g.line(n)
	if errors.Is(err, errNotOpen) {
		return openRecordedStack(cfg, repo, slug, n, prompt, arrive, out, err)
	}
	if err != nil {
		return err
	}
	if len(l.Tips) > 0 {
		return forkError{at: n, fork: l.tip().Number, tips: l.Tips}
	}
	if !l.stacked() {
		return fmt.Errorf("#%d is not stacked: it is built on no open pull request and none is built on it — `owl pr open %d` reviews it", n, n)
	}
	records, err := readStackRecords(repo)
	if err != nil {
		return err
	}
	rec, found := recordFor(records, slug, l)
	if !found {
		rec = stackRecord{name: stackName(l.tip(), takenStackNames(repo, records)), Repo: slug}
	}
	unlock, err := lockWorkspace(repo, rec.name)
	if err != nil {
		return err
	}
	defer unlock()
	wt, err := ensureStackWorktree(cfg, repo, rec.name, l.tip().Number, out)
	if err != nil {
		return err
	}
	rec.Tip, rec.PRs = l.tip().Number, l.numbers()
	if err := writeStackRecord(repo, rec); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s\n", rec.name, lineArrows(l))
	return stackWorkspace(rec.name, wt, l, stackPrompt(cfg, l)).open(cfg, newWindows(cfg, reviews), repo, prompt, arrive, out)
}

// stackWorkspace is the workspace of a stack review of line l.
func stackWorkspace(name, dir string, l stackLine, first string) workspace {
	return workspace{
		label: name,
		name:  name,
		dir:   dir,
		first: first,
		env: map[string]string{
			"OWL_PR":    strconv.Itoa(l.tip().Number),
			"OWL_STACK": joinNumbers(l.numbers(), ","),
		},
	}
}

// openRecordedStack goes back to the stack workspace that reviewed a
// line GitHub no longer holds open, by the record that lists PR n: what
// → and ↵ on a merged layer's row reach. Only to a worktree still
// there — a merged line has nothing left to review, so none is made
// for it, its record is not moved, and a fresh conversation there is
// handed no review to start. notOpen is the answer without one.
func openRecordedStack(cfg Config, repo, slug string, n int, prompt string, arrive bool, out io.Writer, notOpen error) error {
	records, err := readStackRecords(repo)
	if err != nil {
		return err
	}
	list, err := listWorktrees(repo)
	if err != nil {
		return err
	}
	var recs []stackRecord
	var dirs []string
	for _, r := range records {
		if !strings.EqualFold(r.Repo, slug) || !slices.Contains(r.PRs, n) {
			continue
		}
		for _, wt := range list {
			if !wt.Prunable && wt.handle() == r.name {
				recs, dirs = append(recs, r), append(dirs, wt.Path)
			}
		}
	}
	switch len(recs) {
	case 0:
		return notOpen
	case 1:
	default:
		names := make([]string, len(recs))
		for i, r := range recs {
			names[i] = fmt.Sprintf("%s (to #%d)", r.name, r.Tip)
		}
		return fmt.Errorf("#%d was a layer of %d stack workspaces, %s: open one by its tip", n, len(recs), strings.Join(names, ", "))
	}
	rec := recs[0]
	unlock, err := lockWorkspace(repo, rec.name)
	if err != nil {
		return err
	}
	defer unlock()
	l := stackLine{Repo: slug, At: n}
	for _, p := range rec.PRs {
		l.PRs = append(l.PRs, stackPR{Number: p})
	}
	fmt.Fprintf(out, "%s: %s, no longer open\n", rec.name, lineArrows(l))
	return stackWorkspace(rec.name, dirs[0], l, "").open(cfg, newWindows(cfg, reviews), repo, prompt, arrive, out)
}

// ensureStackWorktree makes sure the stack's worktree exists: detached
// at the tip's head, since a review needs no branch and a copy of the
// tip's would be one more thing for close to delete. One that exists
// is used as it stands, as a review's is — the agent moves it to a new
// head when it re-reviews, and owl never moves it under a running one.
func ensureStackWorktree(cfg Config, repo, name string, tip int, out io.Writer) (string, error) {
	list, err := listWorktrees(repo)
	if err != nil {
		return "", err
	}
	for _, wt := range list {
		if !wt.Prunable && wt.handle() == name {
			return wt.Path, nil
		}
	}
	path := filepath.Join(repo, cfg.WorktreesDir, name)
	if err := excludeFromStatus(repo, cfg.WorktreesDir); err != nil {
		return "", err
	}
	_, _ = git(repo, "worktree", "prune")
	// Fetched into a ref of its own and dropped once the worktree holds
	// the commit: FETCH_HEAD is the clone's, and another open may be
	// fetching into it under a lock of its own.
	ref := "refs/owl/" + name
	fmt.Fprintf(out, "fetching PR #%d, the line's tip, into %s\n", tip, path)
	if _, err := git(repo, "fetch", "--quiet", cfg.Remote, fmt.Sprintf("+pull/%d/head:%s", tip, ref)); err != nil {
		return "", err
	}
	_, err = git(repo, "worktree", "add", "--quiet", "--detach", path, ref)
	if _, derr := git(repo, "update-ref", "-d", ref); derr != nil && err == nil {
		err = derr
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// runCloseStack is `owl pr close --stack [<N>|<name>]`: the stack
// workspace PR N names, the one called name, or the one the caller is
// in. Its record stays, so opening the line again resumes the
// conversation under its name.
func runCloseStack(cfg Config, mx windows, args []string, force bool, out io.Writer) error {
	if len(args) > 1 {
		return usageError("pr close --stack: expected at most one PR number or stack workspace")
	}
	repo, err := mainRepo(".")
	if err != nil {
		return err
	}
	var name string
	switch {
	case len(args) == 1 && isStackName(args[0]):
		name = args[0]
	case len(args) == 1:
		n, err := parsePRNumber(args[0])
		if err != nil {
			return err
		}
		records, err := readStackRecords(repo)
		if err != nil {
			return err
		}
		slug := currentRepo(cfg.Remote)
		// The line as it is now, when GitHub answers; the records alone
		// when it does not.
		g, gerr := repoGraph(repo, slug)
		if gerr != nil {
			g = nil
		}
		if name, err = stackToClose(records, g, slug, repo, n); err != nil {
			return err
		}
	default:
		if name = inferStack(mx, repo, cfg.WorktreesDir); name == "" {
			return usageError("pr close --stack: PR number or stack workspace required (or run it from inside a stack-<…> worktree or window)")
		}
	}
	return closeWorkspace(cfg, mx, name, func(s string) bool { return s == name }, force, out)
}

// stackToClose is the stack workspace PR n names. First by the rule
// open and the stack view find one by: its record's tip on a line
// through n as the line is now, so a layer pushed on top since the
// last open still names it. Then, for a line changed past recognising
// or a graph that could not be read (g nil), by the line the record
// held at its last open. Two workspaces either way name neither.
func stackToClose(records []stackRecord, g *prGraph, slug, repo string, n int) (string, error) {
	pick := func(match func(stackRecord) bool) []stackRecord {
		var out []stackRecord
		for _, r := range records {
			if strings.EqualFold(r.Repo, slug) && match(r) {
				out = append(out, r)
			}
		}
		return out
	}
	var found []stackRecord
	if g != nil {
		on := map[int]bool{}
		for _, lp := range stackLayers(g, n) {
			on[lp.n] = true
		}
		found = pick(func(r stackRecord) bool { return on[r.Tip] })
	}
	if len(found) == 0 {
		found = pick(func(r stackRecord) bool { return slices.Contains(r.PRs, n) })
	}
	switch len(found) {
	case 0:
		return "", nothingToCloseError{label: fmt.Sprintf("#%d stack", n), repo: repo}
	case 1:
		return found[0].name, nil
	}
	slices.SortFunc(found, func(a, b stackRecord) int { return strings.Compare(a.name, b.name) })
	names := make([]string, len(found))
	for i, r := range found {
		names[i] = fmt.Sprintf("%s (to #%d)", r.name, r.Tip)
	}
	return "", fmt.Errorf("#%d is a layer of %d stack workspaces, %s: close one by its name or its tip", n, len(found), strings.Join(names, ", "))
}

// inferStack is the stack workspace the caller is in: the worktree's
// directory, else the multiplexer's current window, else "".
func inferStack(mx windows, repo, worktreesDir string) string {
	if top, err := git(".", "rev-parse", "--show-toplevel"); err == nil {
		if filepath.Dir(top) == filepath.Join(repo, worktreesDir) && isStackName(filepath.Base(top)) {
			return filepath.Base(top)
		}
	}
	if w, ok := mx.Current(); ok && isStackName(w) {
		return w
	}
	return ""
}

// stackPrompt is the first prompt of a fresh stack review.
func stackPrompt(cfg Config, l stackLine) string {
	return strings.NewReplacer(
		"{pr}", strconv.Itoa(l.tip().Number),
		"{prs}", joinNumbers(l.numbers(), " "),
	).Replace(cfg.Agent.StackPrompt)
}

// lineArrows is the line as one line of text: #4969 → … → #4974.
func lineArrows(l stackLine) string {
	names := make([]string, len(l.PRs))
	for i, p := range l.PRs {
		names[i] = "#" + strconv.Itoa(p.Number)
	}
	return strings.Join(names, " → ")
}

func joinNumbers(nums []int, sep string) string {
	s := make([]string, len(nums))
	for i, n := range nums {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, sep)
}
