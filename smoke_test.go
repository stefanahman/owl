package main

import (
	"fmt"
	"os"
	"testing"
)

// TestFetchSmoke exercises fetchPRs and fetchLocal against the real
// environment, chdir'd to whatever PR_OWL_TEST_CWD is set to (or the
// module dir if unset). Not a unit test — a sanity check that the
// message pipeline works end-to-end without a TTY. Needs an
// authenticated gh and network, so it only runs when asked for:
//
//	PR_OWL_SMOKE=1 go test -run TestFetchSmoke -v
//	PR_OWL_SMOKE=1 PR_OWL_TEST_CWD=~/src/some-repo go test -run TestFetchSmoke -v
func TestFetchSmoke(t *testing.T) {
	if os.Getenv("PR_OWL_SMOKE") == "" {
		t.Skip("set PR_OWL_SMOKE=1 to run against the live gh/git/tmux environment")
	}
	if cwd := os.Getenv("PR_OWL_TEST_CWD"); cwd != "" {
		if err := os.Chdir(os.ExpandEnv(cwd)); err != nil {
			t.Fatalf("chdir %s: %v", cwd, err)
		}
	}
	msg := fetchPRs()
	switch v := msg.(type) {
	case errMsg:
		t.Fatalf("fetchPRs errored: %v", v.err)
	case prsMsg:
		if len(v) == 0 {
			t.Log("fetchPRs returned zero PRs (fine if you have none)")
		}
		t.Logf("fetchPRs returned %d PRs from repo %s", len(v), currentRepo())
		for i, pr := range v {
			if i >= 3 {
				break
			}
			t.Logf("  #%d %s (%s) — %s", pr.Number, pr.Title, pr.Author.Login, pr.UpdatedAt)
		}
	default:
		t.Fatalf("fetchPRs returned unexpected type %T", msg)
	}

	me := currentUser()
	t.Logf("currentUser: %q", me)

	merged := fetchMerged()
	if mm, ok := merged.(mergedMsg); ok {
		t.Logf("fetchMerged returned %d PRs (last %s)", len(mm), mergedWindow)
	} else {
		t.Fatalf("fetchMerged returned unexpected type %T", merged)
	}

	// Group counts using the derived status.
	if prs, ok := msg.(prsMsg); ok && me != "" {
		counts := map[ReviewStatus]int{}
		for _, pr := range prs {
			counts[pr.MyReviewStatus(me)]++
		}
		t.Logf("grouping: todo=%d waiting=%d approved=%d",
			counts[StatusTodo], counts[StatusWaitingForYou]+counts[StatusWaitingForAuthor], counts[StatusApproved])
	}

	local := fetchLocal()
	lm, ok := local.(localMsg)
	if !ok {
		t.Fatalf("fetchLocal returned unexpected type %T", local)
	}
	t.Logf("fetchLocal returned %d branches", len(lm))
	i := 0
	for name, ls := range lm {
		if i >= 3 {
			break
		}
		t.Logf("  %s → %s", name, fmt.Sprintf("wt=%v session=%v state=%v", ls.Worktree != "", ls.Session != "", ls.ClaudeState))
		i++
	}
}
