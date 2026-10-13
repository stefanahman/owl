// Stacks: open pull requests built on each other, each one's base the
// head branch of the one below it. A stack review covers one line of
// them, the path from the trunk to one tip, because that is the code
// the tip would merge; docs/design/stacks.md has the reasoning.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// stackPR is an open pull request as a stack sees it: where it sits,
// and enough to name it. The JSON is gh's own field names, the
// vocabulary an agent reading `owl pr stack --json` already speaks.
type stackPR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	URL         string `json:"url"`
	BaseRefName string `json:"baseRefName"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
	IsDraft     bool   `json:"isDraft"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	// IsCrossRepository says the head is a fork's branch. No pull
	// request in this repository is built on it, whatever it is called.
	IsCrossRepository bool `json:"isCrossRepository"`
}

// stackFields is what each pull request is asked for, by both queries.
const stackFields = `number title url baseRefName headRefName headRefOid isDraft isCrossRepository author { login }`

// prGraph is one repository's open pull requests, indexed both ways.
type prGraph struct {
	repo   string // owner/name
	trunk  string // the default branch
	prs    map[int]stackPR
	onHead map[string][]int // same-repository PRs by their head branch
	onBase map[string][]int // every PR by its base branch
}

func newPRGraph(repo, trunk string, prs []stackPR) *prGraph {
	g := &prGraph{repo: repo, trunk: trunk, prs: map[int]stackPR{},
		onHead: map[string][]int{}, onBase: map[string][]int{}}
	for _, p := range prs {
		if _, dup := g.prs[p.Number]; dup {
			continue // a page boundary that moved under the search
		}
		g.prs[p.Number] = p
		if !p.IsCrossRepository {
			g.onHead[p.HeadRefName] = append(g.onHead[p.HeadRefName], p.Number)
		}
		g.onBase[p.BaseRefName] = append(g.onBase[p.BaseRefName], p.Number)
	}
	for _, list := range g.onHead {
		slices.Sort(list)
	}
	for _, list := range g.onBase {
		slices.Sort(list)
	}
	return g
}

// below is the pull request n is built on: the one whose head branch
// is n's base. 0 when there is none, and n sits on the trunk or on a
// branch no open pull request carries. GitHub allows one branch to be
// the head of two pull requests into two bases, and then the layer
// below is not one PR: that is an error naming both.
func (g *prGraph) below(n int) (int, error) {
	base := g.prs[n].BaseRefName
	if base == g.trunk {
		// The bottom, whatever else is open: a release PR from the trunk
		// into production carries the trunk as its head, and is no layer
		// under every pull request into it.
		return 0, nil
	}
	switch on := g.onHead[base]; len(on) {
	case 0:
		return 0, nil
	case 1:
		return on[0], nil
	default:
		return 0, fmt.Errorf("#%d's base %s is the head of %s: owl cannot tell which one it is built on", n, base, prList(on))
	}
}

// above is every pull request built on n, by number. A fork's branch
// is never a base here, whatever it is called, and neither is the
// trunk: what targets it is no layer of a PR from it.
func (g *prGraph) above(n int) []int {
	p := g.prs[n]
	if p.IsCrossRepository || p.HeadRefName == g.trunk {
		return nil
	}
	return g.onBase[p.HeadRefName]
}

// tips is every pull request above n with nothing above it, depth
// first and by number: the ends of the lines that pass through n.
func (g *prGraph) tips(n int) []int {
	var out []int
	seen := map[int]bool{n: true}
	var walk func(int)
	walk = func(m int) {
		for _, up := range g.above(m) {
			if seen[up] {
				continue // a base and a head that name each other
			}
			seen[up] = true
			if len(g.above(up)) == 0 {
				out = append(out, up)
				continue
			}
			walk(up)
		}
	}
	walk(n)
	return out
}

// stackLine is the line through a pull request: from the bottom up to
// the tip when there is one tip above, or up to where it forks.
type stackLine struct {
	Repo  string `json:"repo"`
	Trunk string `json:"trunk"`
	// Base is the bottom's base: the trunk, or a branch no open pull
	// request carries.
	Base string    `json:"base"`
	At   int       `json:"at"`  // the pull request asked about
	PRs  []stackPR `json:"prs"` // bottom first
	// Tips are the ends above PRs' last when the line forks there: the
	// lines a stack review could take. Empty when the last is the tip.
	Tips []stackPR `json:"tips,omitempty"`
}

// errNotOpen is a pull request the graph does not hold: merged,
// closed, or not in this repository at all.
var errNotOpen = errors.New("not an open pull request")

// line is the line through n.
func (g *prGraph) line(n int) (stackLine, error) {
	if _, ok := g.prs[n]; !ok {
		return stackLine{}, fmt.Errorf("#%d in %s: %w", n, g.repo, errNotOpen)
	}
	l := stackLine{Repo: g.repo, Trunk: g.trunk, At: n}
	seen := map[int]bool{n: true}
	down := []int{n}
	for m := n; ; {
		b, err := g.below(m)
		if err != nil {
			return stackLine{}, err
		}
		if b == 0 || seen[b] {
			break
		}
		seen[b] = true
		down = append(down, b)
		m = b
	}
	slices.Reverse(down)
	nums := down
	for m := n; ; {
		up := g.above(m)
		if len(up) > 1 {
			for _, t := range g.tips(m) {
				l.Tips = append(l.Tips, g.prs[t])
			}
			break
		}
		if len(up) == 0 || seen[up[0]] {
			break
		}
		seen[up[0]] = true
		nums = append(nums, up[0])
		m = up[0]
	}
	for _, m := range nums {
		l.PRs = append(l.PRs, g.prs[m])
	}
	l.Base = l.PRs[0].BaseRefName
	return l, nil
}

// tip is the line's last pull request, the one whose tree holds every
// layer.
func (l stackLine) tip() stackPR { return l.PRs[len(l.PRs)-1] }

// stacked reports whether the pull request is built on another, or
// another on it.
func (l stackLine) stacked() bool { return len(l.PRs) > 1 || len(l.Tips) > 0 }

// numbers are the line's pull requests, bottom first.
func (l stackLine) numbers() []int {
	out := make([]int, len(l.PRs))
	for i, p := range l.PRs {
		out[i] = p.Number
	}
	return out
}

// layerPlace is a layer in a stack's tree: the pull request, and what
// goes before its title, in the stack view, to show where it branches.
type layerPlace struct {
	n      int
	prefix string
}

// stackLayers are the layers of every line through at, in tree order:
// the path from the bottom up to at, then everything above it, a fork's
// branches drawn as a tree. Layers on lines that do not pass through
// at — a sibling off a lower layer — are not its stack.
func stackLayers(g *prGraph, at int) []layerPlace {
	l, err := g.line(at)
	if err != nil {
		return nil
	}
	var out []layerPlace
	seen := map[int]bool{}
	for _, p := range l.PRs {
		out = append(out, layerPlace{n: p.Number})
		seen[p.Number] = true
		if p.Number == at {
			break
		}
	}
	var walk func(n int, prefix string)
	walk = func(n int, prefix string) {
		up := g.above(n)
		if len(up) == 1 {
			if !seen[up[0]] {
				seen[up[0]] = true
				out = append(out, layerPlace{n: up[0], prefix: prefix})
				walk(up[0], prefix)
			}
			return
		}
		for i, c := range up {
			if seen[c] {
				continue
			}
			seen[c] = true
			branch, under := "├─ ", "│  "
			if i == len(up)-1 {
				branch, under = "└─ ", "   "
			}
			out = append(out, layerPlace{n: c, prefix: prefix + branch})
			walk(c, prefix+under)
		}
	}
	walk(at, "")
	return out
}

// prList names pull requests for a message: #1, #2 and #3.
func prList(nums []int) string {
	names := make([]string, len(nums))
	for i, n := range nums {
		names[i] = "#" + strconv.Itoa(n)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// repoGraphQuery lists a repository's open pull requests, a hundred a
// page. The repository's own list and not a search: a search lags the
// index by seconds, and a pull request opened a moment ago is exactly
// the layer someone is about to ask for.
const repoGraphQuery = `query($owner: String!, $name: String!, $endCursor: String) {
  repository(owner: $owner, name: $name) {
    defaultBranchRef { name }
    pullRequests(states: OPEN, first: 100, after: $endCursor) {
      pageInfo { hasNextPage endCursor }
      nodes { ` + stackFields + ` }
    }
  }
}`

// repoGraph is the open pull requests of slug (owner/name), asked from
// dir, whose remotes gh would otherwise guess by.
func repoGraph(dir, slug string) (*prGraph, error) {
	owner, name, ok := strings.Cut(slug, "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("%q is not owner/name", slug)
	}
	out, err := runOut(ghIn(dir, "api", "graphql", "--paginate",
		"-f", "query="+repoGraphQuery, "-f", "owner="+owner, "-f", "name="+name))
	if err != nil {
		return nil, err
	}
	var trunk string
	var prs []stackPR
	err = eachPage(out, func(raw []byte) error {
		var page struct {
			Data struct {
				Repository *struct {
					DefaultBranchRef struct {
						Name string `json:"name"`
					} `json:"defaultBranchRef"`
					PullRequests struct {
						Nodes []stackPR `json:"nodes"`
					} `json:"pullRequests"`
				} `json:"repository"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		r := page.Data.Repository
		if r == nil {
			return fmt.Errorf("no repository %s", slug)
		}
		trunk = r.DefaultBranchRef.Name
		prs = append(prs, r.PullRequests.Nodes...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open pull requests of %s: %w", slug, err)
	}
	return newPRGraph(slug, trunk, prs), nil
}

// eachPage hands each JSON document of a `gh api --paginate` answer to
// fn. gh prints one document per page, one after the other.
func eachPage(out string, fn func([]byte) error) error {
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if err := fn(raw); err != nil {
			return err
		}
	}
}

// reviewNotesDir is where sessions reviewing slug's pull requests
// leave each other notes: pr-<N>.md from a review of one PR, and
// <workspace>.md from a stack review. One writer per file.
func reviewNotesDir(slug string) (string, error) {
	dir, err := stateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "reviews", filepath.FromSlash(slug)), nil
}

// runStack is `owl pr stack <N> [--json]`: the line through N.
func runStack(cfg Config, args []string, out io.Writer) error {
	var num string
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return usageError("pr stack: unknown flag " + a)
		case num == "":
			num = a
		default:
			return usageError("pr stack: unexpected argument " + a)
		}
	}
	if num == "" {
		return usageError("pr stack: PR number required")
	}
	n, err := parsePRNumber(num)
	if err != nil {
		return err
	}
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
	if err != nil {
		return err
	}
	notes, err := reviewNotesDir(slug)
	if err != nil {
		return err
	}
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			stackLine
			Notes string `json:"notes"`
		}{l, notes})
	}
	printLine(out, l)
	fmt.Fprintf(out, "notes: %s\n", notes)
	return nil
}

// printLine writes the line as `owl pr stack` shows it: the base, one
// row per layer with the asked-about one marked, and the tips where it
// forks.
func printLine(out io.Writer, l stackLine) {
	on := "on " + l.Base
	if l.Base != l.Trunk {
		on += ", which is not " + l.Trunk + " and no open pull request's branch"
	}
	fmt.Fprintf(out, "%s, %s\n", l.Repo, on)
	for _, p := range l.PRs {
		mark := " "
		if p.Number == l.At {
			mark = "▸"
		}
		fmt.Fprintf(out, "%s #%-5d %s%s\n", mark, p.Number, p.Title, draftMark(p.IsDraft))
	}
	if len(l.Tips) > 0 {
		fmt.Fprintf(out, "  it forks above #%d, into %d lines that end at:\n", l.tip().Number, len(l.Tips))
		for _, p := range l.Tips {
			fmt.Fprintf(out, "    #%-5d %s%s\n", p.Number, p.Title, draftMark(p.IsDraft))
		}
	}
}

func draftMark(draft bool) string {
	if draft {
		return " [draft]"
	}
	return ""
}

// scopeGraphQuery is every open pull request a list's scope spans,
// with the repository each is in: the PR list asks across all of them
// at once, where the repository query would take one call per repo.
const scopeGraphQuery = `query($q: String!, $endCursor: String) {
  search(query: $q, type: ISSUE_ADVANCED, first: 100, after: $endCursor) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { ` + stackFields + ` repository { nameWithOwner defaultBranchRef { name } } } }
  }
}`

// scopeGraphs is one graph per repository the scope's open pull
// requests are in.
func scopeGraphs(scope string) (map[string]*prGraph, error) {
	out, err := runOut(exec.Command("gh", "api", "graphql", "--paginate",
		"-f", "query="+scopeGraphQuery, "-f", "q="+scope+" is:pr is:open"))
	if err != nil {
		return nil, err
	}
	type node struct {
		stackPR
		Repository struct {
			NameWithOwner    string `json:"nameWithOwner"`
			DefaultBranchRef struct {
				Name string `json:"name"`
			} `json:"defaultBranchRef"`
		} `json:"repository"`
	}
	byRepo := map[string][]stackPR{}
	trunks := map[string]string{}
	err = eachPage(out, func(raw []byte) error {
		var page struct {
			Data struct {
				Search struct {
					Nodes []node `json:"nodes"`
				} `json:"search"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, n := range page.Data.Search.Nodes {
			repo := n.Repository.NameWithOwner
			if repo == "" || n.Number == 0 {
				continue // a search hit that is not a pull request
			}
			byRepo[repo] = append(byRepo[repo], n.stackPR)
			trunks[repo] = n.Repository.DefaultBranchRef.Name
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open pull requests of %s: %w", scope, err)
	}
	graphs := make(map[string]*prGraph, len(byRepo))
	for repo, prs := range byRepo {
		graphs[repo] = newPRGraph(repo, trunks[repo], prs)
	}
	return graphs, nil
}

// stackGraphCache is a graph as the list's cache holds it.
type stackGraphCache struct {
	Repo  string    `json:"repo"`
	Trunk string    `json:"trunk"`
	PRs   []stackPR `json:"prs"`
}

// cacheGraphs is the graphs in the cache's shape, in a stable order
// so the file does not churn on the map's.
func cacheGraphs(graphs map[string]*prGraph) []stackGraphCache {
	out := make([]stackGraphCache, 0, len(graphs))
	for _, repo := range slices.Sorted(maps.Keys(graphs)) {
		g := graphs[repo]
		c := stackGraphCache{Repo: g.repo, Trunk: g.trunk}
		for _, n := range slices.Sorted(maps.Keys(g.prs)) {
			c.PRs = append(c.PRs, g.prs[n])
		}
		out = append(out, c)
	}
	return out
}

// graphsFromCache is cacheGraphs read back; nil for a cache from
// before owl kept them.
func graphsFromCache(cached []stackGraphCache) map[string]*prGraph {
	if len(cached) == 0 {
		return nil
	}
	graphs := make(map[string]*prGraph, len(cached))
	for _, c := range cached {
		graphs[c.Repo] = newPRGraph(c.Repo, c.Trunk, c.PRs)
	}
	return graphs
}
