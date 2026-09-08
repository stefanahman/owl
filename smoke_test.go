package main

import (
	"fmt"
	"os"
	"testing"
)

// TestFetchSmoke exercises fetchPRs and fetchLocal against the real
// environment, chdir'd to whatever OWL_TEST_CWD is set to (or the
// module dir if unset). Not a unit test — a sanity check that the
// message pipeline works end-to-end without a TTY. Needs an
// authenticated gh and network, so it only runs when asked for:
//
//	OWL_SMOKE=1 go test -run TestFetchSmoke -v
//	OWL_SMOKE=1 OWL_TEST_CWD=~/src/some-repo go test -run TestFetchSmoke -v
func TestFetchSmoke(t *testing.T) {
	if os.Getenv("OWL_SMOKE") == "" {
		t.Skip("set OWL_SMOKE=1 to run against the live gh/git/tmux environment")
	}
	if cwd := os.Getenv("OWL_TEST_CWD"); cwd != "" {
		if err := os.Chdir(os.ExpandEnv(cwd)); err != nil {
			t.Fatalf("chdir %s: %v", cwd, err)
		}
	}
	m := model{cfg: defaultConfig()}
	m.repo = currentRepo(m.cfg.Remote)
	msg := m.fetchPRs()
	switch v := msg.(type) {
	case errMsg:
		t.Fatalf("fetchPRs errored: %v", v.err)
	case prsMsg:
		if len(v.prs) == 0 {
			t.Log("fetchPRs returned zero PRs (fine if you have none)")
		}
		t.Logf("fetchPRs returned %d PRs from repo %s", len(v.prs), m.repo)
		for i, pr := range v.prs {
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

	merged := m.fetchMerged()
	if mm, ok := merged.(mergedMsg); ok {
		t.Logf("fetchMerged returned %d PRs (last %s)", len(mm.prs), mergedWindow)
	} else {
		t.Fatalf("fetchMerged returned unexpected type %T", merged)
	}

	// Group counts using the derived status.
	if prs, ok := msg.(prsMsg); ok && me != "" {
		counts := map[ReviewStatus]int{}
		for _, pr := range prs.prs {
			counts[pr.MyReviewStatus(me)]++
		}
		t.Logf("grouping: todo=%d waiting=%d approved=%d",
			counts[StatusTodo], counts[StatusWaitingForYou]+counts[StatusWaitingForAuthor], counts[StatusApproved])
	}

	local := m.fetchLocal()
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
		t.Logf("  %s → %s", name, fmt.Sprintf("wt=%v window=%v state=%v", ls.Worktree != "", ls.Window != "", ls.ClaudeState))
		i++
	}
}
