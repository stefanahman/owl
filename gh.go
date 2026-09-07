// Wrapper for the `gh` CLI, scoped to the repo of the working directory
// pr-owl was launched from.
package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// mergedWindow is how far back to fetch merged PRs. Big enough to
// cover a day of team activity so "merged without my review" cases
// stay visible for audit; short enough to keep the list from turning
// into a firehose.
const mergedWindow = 24 * time.Hour

// PR mirrors the JSON shape returned by `gh pr list --json ...`.
// Fields intentionally kept minimal to keep the query fast.
type PR struct {
	Number      int    `json:"number"`
	Title       string `json:"title"`
	Body        string `json:"body"` // searched by links' patterns (ticket ids)
	URL         string `json:"url"`  // the PR's page; correct host on GitHub Enterprise too
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid,omitempty"` // populated by graphql fetch; drives staleness detection
	UpdatedAt   string `json:"updatedAt"`
	MergedAt    string `json:"mergedAt,omitempty"` // populated for merged PRs
	IsDraft     bool   `json:"isDraft"`

	Author struct {
		Login string `json:"login"`
	} `json:"author"`

	// Reviews is the full review history for this PR (chronological).
	// We use the full history rather than `latestReviews` because
	// `latestReviews` is truly the latest per user — including COMMENTED
	// — which flips a PR's verdict when a reviewer approves and then
	// leaves a follow-up comment ("my approval stands"). Correct
	// derivation requires walking history and ignoring COMMENTED when
	// determining verdict state.
	Reviews []Review `json:"reviews"`
}

// Review is a subset of a GitHub PR review. State values: APPROVED,
// CHANGES_REQUESTED, COMMENTED, DISMISSED.
type Review struct {
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	State       string `json:"state"`
	SubmittedAt string `json:"submittedAt"`

	// Commit.OID is populated only by the graphql fetch (fetchPRs). gh's
	// `pr list --json reviews` returns commit.oid as empty string, so on
	// merged rows (fetched via fetchMerged which stays on gh pr list)
	// this stays empty — merged rows don't need staleness detection.
	Commit struct {
		OID string `json:"oid"`
	} `json:"commit"`
}

// ReviewStatus is where a PR sits in the reviewer's own workflow.
type ReviewStatus int

const (
	StatusTodo             ReviewStatus = iota // I haven't submitted anything (or dismissed)
	StatusWaitingForYou                        // I engaged; author pushed after my last review — my turn to re-look
	StatusWaitingForAuthor                     // I engaged; my last review still covers head — author's turn
	StatusApproved                             // I approved; approval covers current head
)

func (s ReviewStatus) String() string {
	switch s {
	case StatusTodo:
		return "todo"
	case StatusWaitingForYou:
		return "waiting for you"
	case StatusWaitingForAuthor:
		return "waiting for author"
	case StatusApproved:
		return "approved"
	default:
		return "unknown"
	}
}

// latestVerdictsPerUser returns each reviewer's most recent
// non-COMMENTED review — their current verdict (APPROVED /
// CHANGES_REQUESTED / DISMISSED). COMMENTED reviews are transient
// feedback and don't shift the verdict.
//
// This is GitHub's own model for "is this reviewer currently blocking /
// approving": a follow-up comment doesn't retract an earlier approval.
func (p PR) latestVerdictsPerUser() map[string]Review {
	out := map[string]Review{}
	for _, r := range p.Reviews {
		if r.State == "COMMENTED" {
			continue
		}
		cur, ok := out[r.Author.Login]
		if !ok || parsedTime(cur.SubmittedAt).Before(parsedTime(r.SubmittedAt)) {
			out[r.Author.Login] = r
		}
	}
	return out
}

// parsedTime parses an RFC3339 timestamp, returning the zero Time on
// error. In practice GitHub returns Z-suffixed UTC so a naive string
// compare would work — but parsing avoids the trap when a non-Z
// timezone ever slips through, and reads what it means.
func parsedTime(iso string) time.Time {
	t, _ := time.Parse(time.RFC3339, iso)
	return t
}

// IApproved reports whether my current verdict is APPROVED.
func (p PR) IApproved(me string) bool {
	return p.latestVerdictsPerUser()[me].State == "APPROVED"
}

// IReviewed reports whether I submitted any review other than
// DISMISSED — includes COMMENTED, since commenting IS engagement.
// True for both approved and merely-engaged; combine with IApproved
// to distinguish the two.
func (p PR) IReviewed(me string) bool {
	for _, r := range p.Reviews {
		if r.Author.Login == me && r.State != "DISMISSED" {
			return true
		}
	}
	return false
}

// HasChangesRequested reports whether any reviewer's current verdict
// is CHANGES_REQUESTED — the PR is blocked pending author response,
// regardless of who did the blocking.
func (p PR) HasChangesRequested() bool {
	for _, r := range p.latestVerdictsPerUser() {
		if r.State == "CHANGES_REQUESTED" {
			return true
		}
	}
	return false
}

// MyReviewStatus derives where I sit on this PR.
//
// Rules (strict staleness — an approval covers a specific tree, not
// the PR forever):
//
//  1. No engagement, or my most recent review is DISMISSED → Todo.
//  2. My most recent review (any state) is on a different commit than
//     the current head → Waiting for you (re-review needed — author
//     pushed after my last look, regardless of what verdict I gave).
//  3. My verdict is APPROVED and my review covers head → Approved.
//  4. Otherwise (I engaged, review covers head, no approval) →
//     Waiting for author.
//
// "My most recent review" means the latest of any state including
// COMMENTED — because if I commented after a push, my anchor for
// "have I looked at the current tree" is that comment's commit.
//
// Needs Reviews[i].Commit.OID + PR.HeadRefOid populated (graphql
// fetch only — gh's `pr list --json` returns commit.oid empty).
func (p PR) MyReviewStatus(me string) ReviewStatus {
	latest := latestReviewByMe(p.Reviews, me)
	if latest == nil || latest.State == "DISMISSED" {
		return StatusTodo
	}
	stale := latest.Commit.OID != "" && p.HeadRefOid != "" && latest.Commit.OID != p.HeadRefOid
	if stale {
		return StatusWaitingForYou
	}
	if p.latestVerdictsPerUser()[me].State == "APPROVED" {
		return StatusApproved
	}
	return StatusWaitingForAuthor
}

// latestReviewByMe returns my most recent review of any state, or nil
// if I never reviewed. Used by MyReviewStatus to anchor staleness on
// my last activity (comment or verdict), not just my last verdict.
func latestReviewByMe(reviews []Review, me string) *Review {
	var latest *Review
	for i := range reviews {
		r := &reviews[i]
		if r.Author.Login != me {
			continue
		}
		if latest == nil || parsedTime(latest.SubmittedAt).Before(parsedTime(r.SubmittedAt)) {
			latest = r
		}
	}
	return latest
}

// repoURLRe extracts owner/name from a remote URL that has a host:
// scp-style (`git@github.com:owner/name.git`) or with a scheme
// (`https://github.com/owner/name`, `ssh://git@host/owner/name`), with
// or without the `.git` suffix. A local path is not a GitHub repo and
// doesn't match. Captures owner ($1) and name ($2).
var repoURLRe = regexp.MustCompile(`^(?:[a-z][a-z0-9+.-]*://[^/]+/|[^/:]+:)([^/:]+)/([^/]+?)(?:\.git)?/?$`)

// parseRepoURL returns owner/name for a remote URL, or "".
func parseRepoURL(url string) string {
	m := repoURLRe.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return ""
	}
	return m[1] + "/" + m[2]
}

// currentRepo returns the owner/name of the repo in the working
// directory, or "" if the cwd isn't a git repo with the configured
// GitHub remote (`remote`, origin by default — `upstream` in a
// fork-based workflow).
//
// Reads the URL as configured (`remote.<name>.url`, ~10ms) rather than
// asking `gh repo view` (~700-1000ms — gh is a large binary that
// initializes config/HTTP/auth even for non-network subcommands); this
// is on the blocking path from initialModel(), so the difference is
// user-visible as popup startup latency. The configured URL, not
// `remote get-url`'s rewritten one: an `insteadOf` rule may point the
// fetch elsewhere, the repo is still the one named in the URL.
func currentRepo(remote string) string {
	out, err := exec.Command("git", "config", "--get", "remote."+remote+".url").Output()
	if err != nil {
		return ""
	}
	return parseRepoURL(string(out))
}

// currentUser returns the authenticated user's login (e.g. "stefanahman").
// Returns "" on failure — grouping falls back to "todo" for everything.
func currentUser() string {
	out, err := exec.Command("gh", "api", "user", "-q", ".login").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// prSearchQuery is the shared search filter used by fetchPRs and
// fetchMerged so they always agree on the row set.
//
// The OR keeps PRs visible past your review submission: `review-requested:@me`
// alone drops a PR the moment you submit (GitHub clears the request),
// hiding exactly the PRs you want to keep watching for follow-up
// commits. `reviewed-by` covers that gap.
//
// `-author:@me` excludes your own PRs — pr-owl is for tracking PRs you
// need to review, and you can't review your own. Without this,
// self-comments on your own PRs put them in `reviewed-by:@me`.
const prSearchQuery = "(review-requested:@me OR reviewed-by:@me) -author:@me"

// fetchPRs returns the open PRs in the current repo where you are or
// were a reviewer (per prSearchQuery).
//
// Uses the GitHub GraphQL API (via `gh api graphql`) rather than
// `gh pr list --json` because the latter returns each review's
// `commit.oid` as an empty string — and pr-owl needs those oids to
// detect stale reviews (my review is on a different commit than the
// current head, so re-review is needed). GraphQL populates them.
//
// The search type is ISSUE_ADVANCED, the one `gh pr list --search`
// uses itself: the classic ISSUE search returns nothing for an OR
// between qualifiers, ISSUE_ADVANCED returns the union.
func (m model) fetchPRs() tea.Msg {
	gen := m.fetchGen // the round this fetch belongs to
	repo := m.repo
	if repo == "" {
		return errMsg{gen, fmt.Errorf("no GitHub repo: the working directory has no %q remote", m.cfg.Remote)}
	}

	query := `
query($q: String!) {
  search(query: $q, type: ISSUE_ADVANCED, first: 100) {
    nodes {
      ... on PullRequest {
        number title body url headRefName headRefOid updatedAt isDraft
        author { login }
        reviews(last: 100) {
          nodes { author { login } state submittedAt commit { oid } }
        }
      }
    }
  }
}`
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query="+query,
		"-f", fmt.Sprintf("q=repo:%s is:pr is:open %s", repo, prSearchQuery),
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return errMsg{gen, fmt.Errorf("gh api graphql: %s", ee.Stderr)}
		}
		return errMsg{gen, fmt.Errorf("gh api graphql: %w", err)}
	}

	type prNode struct {
		Number      int    `json:"number"`
		Title       string `json:"title"`
		Body        string `json:"body"`
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
	}
	var resp struct {
		Data struct {
			Search struct {
				Nodes []prNode `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return errMsg{gen, fmt.Errorf("parse graphql: %w", err)}
	}

	prs := make([]PR, 0, len(resp.Data.Search.Nodes))
	for _, n := range resp.Data.Search.Nodes {
		pr := PR{
			Number:      n.Number,
			Title:       n.Title,
			Body:        n.Body,
			URL:         n.URL,
			HeadRefName: n.HeadRefName,
			HeadRefOid:  n.HeadRefOid,
			UpdatedAt:   n.UpdatedAt,
			IsDraft:     n.IsDraft,
			Reviews:     n.Reviews.Nodes,
		}
		pr.Author.Login = n.Author.Login
		prs = append(prs, pr)
	}
	return prsMsg{gen, prs}
}

// fetchMerged returns PRs merged in the last mergedWindow, so a PR you
// reviewed doesn't vanish from view the instant it merges.
func (m model) fetchMerged() tea.Msg {
	gen := m.fetchGen
	cutoff := time.Now().Add(-mergedWindow).UTC().Format("2006-01-02T15:04:05Z")
	q := fmt.Sprintf("%s merged:>=%s", prSearchQuery, cutoff)
	prs, err := ghPRList(m.repo, "merged", q)
	if err != nil {
		return errMsg{gen, fmt.Errorf("merged: %w", err)}
	}
	return mergedMsg{gen, prs}
}

// ghPRList runs `gh pr list` against an explicit repo — gh's own
// current-repo guess fails when a clone has several remotes.
func ghPRList(repo, state, search string) ([]PR, error) {
	cmd := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--search", search,
		"--state", state,
		"--json", "number,title,body,url,headRefName,author,updatedAt,mergedAt,isDraft,reviews",
		"--limit", "100",
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("%s", ee.Stderr)
		}
		return nil, err
	}
	var prs []PR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return prs, nil
}
