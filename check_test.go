package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pr builds a PR with one review by me, so MyReviewStatus is decided
// by whether that review covers the current head.
func reviewed(n int, title, head string, mine ...Review) PR {
	p := PR{Number: n, Title: title, HeadRefOid: head}
	p.Reviews = mine
	return p
}

func myReview(state, onCommit string) Review {
	r := Review{State: state, SubmittedAt: time.Now().Add(-time.Hour).Format(time.RFC3339)}
	r.Author.Login = "me"
	r.Commit.OID = onCommit
	return r
}

func TestArrived(t *testing.T) {
	const me = "me"
	todo := reviewed(1, "never looked at", "aaa")                             // no review: Todo
	approved := reviewed(2, "signed off", "bbb", myReview("APPROVED", "bbb")) // covers head: Approved
	// The author pushed after my review: it no longer covers the head.
	stale := reviewed(3, "author pushed", "ccc", myReview("APPROVED", "bbb"))

	if !inYourCourt(todo, me) || inYourCourt(approved, me) || !inYourCourt(stale, me) {
		t.Fatalf("in-your-court: todo %v, approved %v, stale %v",
			inYourCourt(todo, me), inYourCourt(approved, me), inYourCourt(stale, me))
	}

	// Nothing was there before: both of the waiting ones are news.
	got := arrived(nil, []PR{todo, approved, stale}, me)
	if len(got) != 2 || got[0].Number != 3 || got[1].Number != 1 {
		t.Errorf("from an empty baseline: %+v", got)
	}
	// Seen already: a PR that has sat in Todo since the last look is not
	// news, however long it sits there.
	if got := arrived([]PR{todo, approved}, []PR{todo, approved}, me); len(got) != 0 {
		t.Errorf("standing work announced again: %+v", got)
	}
	// The transition is what counts: approved yesterday, pushed to since.
	if got := arrived([]PR{approved}, []PR{reviewed(2, "signed off", "zzz", myReview("APPROVED", "bbb"))}, me); len(got) != 1 || got[0].Number != 2 {
		t.Errorf("a push after approval did not read as arriving: %+v", got)
	}
	// Leaving your court is not news either.
	if got := arrived([]PR{todo}, []PR{reviewed(1, "never looked at", "aaa", myReview("APPROVED", "aaa"))}, me); len(got) != 0 {
		t.Errorf("approving something announced it: %+v", got)
	}
}

func TestCheckFirstRunIsSilent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	// A cold cache is a baseline, not a morning of news. Prove it by
	// the file: nothing to compare against, so nothing is announced,
	// and what would have been announced is what is written down.
	if loadCache("acme/app") != nil {
		t.Fatal("a cache from nowhere")
	}
	saveCache("acme/app", cacheFile{Prs: []PR{reviewed(1, "x", "aaa")}, Me: "me", FetchedAt: time.Now()})
	if _, err := os.Stat(filepath.Join(dir, "owl", "acme-app.json")); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	// With that baseline, the same PR is no longer news.
	if got := arrived(loadCache("acme/app").Prs, []PR{reviewed(1, "x", "aaa")}, "me"); len(got) != 0 {
		t.Errorf("the baseline did not silence it: %+v", got)
	}
}

func TestCheckKeepsWhatIsTheListsAlone(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// The list left a merged section and a cursor behind. A check runs
	// from a scheduler, behind your back, and must not lose either.
	saveCache("acme/app", cacheFile{
		Prs:    []PR{reviewed(1, "x", "aaa")},
		Merged: []PR{reviewed(9, "landed", "zzz")},
		Me:     "me", Cursor: 4, FetchedAt: time.Now().Add(-time.Hour),
	})
	cached := loadCache("acme/app")
	updated := *cached
	updated.Prs, updated.Me, updated.FetchedAt = []PR{reviewed(2, "new", "bbb")}, "me", time.Now()
	saveCache("acme/app", updated)

	got := loadCache("acme/app")
	if len(got.Merged) != 1 || got.Merged[0].Number != 9 {
		t.Errorf("the merged section was lost: %+v", got.Merged)
	}
	if got.Cursor != 4 {
		t.Errorf("the cursor moved to %d", got.Cursor)
	}
	if len(got.Prs) != 1 || got.Prs[0].Number != 2 {
		t.Errorf("the PRs did not update: %+v", got.Prs)
	}
}

func TestAttentionHookGetsTheNumbers(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "hook.out")
	cfg := defaultConfig()
	cfg.Mux = "tmux"
	cfg.Hooks.Attention = hookByMux{"": "printf '%s|%s|%s|%s\\n' \"$OWL_ARRIVED\" \"$OWL_ARRIVED_PRS\" \"$OWL_WAITING\" \"$OWL_REPO\" > " + out}

	news := []PR{reviewed(7, "second", "b"), reviewed(4, "first", "a")}
	if err := announce(cfg, "acme/app", news, 5, os.Stdout); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "2|7,4|5|acme/app" {
		t.Errorf("hook environment = %q", got)
	}

	// Nothing arrived, but the standing count is still true and the
	// hook still hears it — a badge that can go up has to come down.
	if err := announce(cfg, "acme/app", nil, 0, os.Stdout); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "0||0|acme/app" {
		t.Errorf("hook on a quiet run = %q", got)
	}
}
