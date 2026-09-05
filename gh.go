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

	tea "github.com/charmbracelet/bubbletea"
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

// repoURLRe extracts owner/name from a GitHub remote URL. Matches both
// SSH (`git@github.com:owner/name.git`) and HTTPS
// (`https://github.com/owner/name.git`) shapes, with or without the
// `.git` suffix. Captures owner ($1) and name ($2).
var repoURLRe = regexp.MustCompile(`[:/]([^/]+)/([^/]+?)(?:\.git)?$`)

// currentRepo returns the owner/name of the repo in the working
// directory, or "" if the cwd isn't a git repo with a GitHub `origin`
// remote.
//
// Uses `git remote get-url origin` (reads `.git/config` locally, ~10ms)
// rather than `gh repo view` (~700-1000ms — gh is a large binary that
// initializes config/HTTP/auth even for non-network subcommands). This
// is on the blocking path from initialModel(), so the difference is
// user-visible as popup startup latency.
func currentRepo() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	m := repoURLRe.FindStringSubmatch(strings.TrimSpace(string(out)))
	if len(m) != 3 {
		return ""
	}
	return m[1] + "/" + m[2]
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

// fetchPRs returns the list of open PRs in the current repo where you
// are or were a reviewer (per prSearchQuery).
//
// Uses the GitHub GraphQL API (via `gh api graphql`) rather than
// `gh pr list --json` because the latter returns each review's
// `commit.oid` as an empty string — and pr-owl needs those oids to
// detect stale reviews (my review is on a different commit than the
// current head, so re-review is needed). GraphQL populates them.
//
// Why two sub-queries in one call: GitHub's search API doesn't accept
// AND/OR between qualifiers — `review-requested:@me OR reviewed-by:@me`
// returns zero regardless of parentheses or literal usernames (verified,
// see GitHub Community discussion #181066). `gh pr list --search`
// wasn't hitting the search API — it lists all pulls and applies OR
// client-side — but that path doesn't populate commit.oid.
//
// Fix: one GraphQL request with TWO aliased `search` fields —
// `requested` and `reviewed` — executed server-side, returned under
// their alias keys. One HTTP round-trip, dedupe in Go by PR number.
// Standard GraphQL alias pattern (graphql.org/learn/queries).
//
// Scope: reads currentRepo() from the working directory and adds a
// `repo:<owner>/<name>` filter to both searches.
func fetchPRs() tea.Msg {
	repo := currentRepo()
	if repo == "" {
		return errMsg{fmt.Errorf("gh api graphql: no repo detected in cwd")}
	}

	// $r and $v are the two search queries; the shared PR-shape
	// sub-selection is duplicated below because GraphQL fragment
	// inheritance on aliased fields adds boilerplate without saving
	// bytes on the wire.
	query := `
query($r: String!, $v: String!) {
  requested: search(query: $r, type: ISSUE, first: 100) {
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
  reviewed: search(query: $v, type: ISSUE, first: 100) {
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
	base := fmt.Sprintf("repo:%s is:pr is:open -author:@me", repo)
	cmd := exec.Command("gh", "api", "graphql",
		"-f", "query="+query,
		"-f", "r="+base+" review-requested:@me",
		"-f", "v="+base+" reviewed-by:@me",
	)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return errMsg{fmt.Errorf("gh api graphql: %s", ee.Stderr)}
		}
		return errMsg{fmt.Errorf("gh api graphql: %w", err)}
	}

	// Both aliased results carry the same PR shape.
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
	type searchResult struct {
		Nodes []prNode `json:"nodes"`
	}
	var resp struct {
		Data struct {
			Requested searchResult `json:"requested"`
			Reviewed  searchResult `json:"reviewed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return errMsg{fmt.Errorf("parse graphql: %w", err)}
	}

	// Dedupe by PR number, first-seen wins.
	seen := make(map[int]bool)
	out2 := make([]PR, 0, len(resp.Data.Requested.Nodes)+len(resp.Data.Reviewed.Nodes))
	for _, list := range [][]prNode{resp.Data.Requested.Nodes, resp.Data.Reviewed.Nodes} {
		for _, n := range list {
			if seen[n.Number] {
				continue
			}
			seen[n.Number] = true
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
			out2 = append(out2, pr)
		}
	}
	return prsMsg(out2)
}

// fetchMerged returns PRs merged in the last mergedWindow, so a PR you
// reviewed doesn't vanish from view the instant it merges.
func fetchMerged() tea.Msg {
	cutoff := time.Now().Add(-mergedWindow).UTC().Format("2006-01-02T15:04:05Z")
	q := fmt.Sprintf("%s merged:>=%s", prSearchQuery, cutoff)
	prs, err := ghPRList("merged", q)
	if err != nil {
		// Best-effort — don't fail the whole app for the merged section.
		return mergedMsg(nil)
	}
	return mergedMsg(prs)
}

// ghPRList is the shared shape for both open + merged queries.
func ghPRList(state, search string) ([]PR, error) {
	cmd := exec.Command("gh", "pr", "list",
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
