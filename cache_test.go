package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCacheRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if loadCache("acme/app") != nil {
		t.Error("no file yet: want nil")
	}
	want := cacheFile{Prs: fixturePRs(), Merged: fixtureMerged(), Me: "stefanahman", FetchedAt: time.Now().UTC().Truncate(time.Second)}
	saveCache("acme/app", want)
	got := loadCache("acme/app")
	if got == nil || len(got.Prs) != len(want.Prs) || got.Me != want.Me || !got.FetchedAt.Equal(want.FetchedAt) {
		t.Fatalf("loadCache = %+v, want %+v", got, want)
	}
	if entries, _ := os.ReadDir(filepath.Dir(cachePath("acme/app"))); len(entries) != 1 {
		t.Errorf("cache dir holds %d entries, want the one file (no temp file left)", len(entries))
	}
	if err := os.WriteFile(cachePath("acme/app"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if loadCache("acme/app") != nil {
		t.Error("a corrupt file must read as no cache")
	}
	if cachePath("") != "" {
		t.Error("no repo, no cache file")
	}
}

// TestCursorResumes: the cursor row is saved with the cache on exit
// and restored at the next start, clamped to the list that is there.
func TestCursorResumes(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := testModel(t)
	m.prs, m.ready, m.cursor = fixturePRs(), true, 3
	m.persistCache()
	if got := loadCache("acme/example"); got == nil || got.Cursor != 3 {
		t.Fatalf("cache cursor = %+v, want 3", got)
	}

	resumed := newModel(defaultConfig(), "acme/example", loadCache("acme/example"))
	resumed.me = "stefanahman"
	if resumed.cursor != 3 || resumed.selectedPR() == nil {
		t.Errorf("resumed cursor = %d (PR %v), want row 3 on a PR", resumed.cursor, resumed.selectedPR())
	}

	beyond := loadCache("acme/example")
	beyond.Cursor = 99
	clamped := newModel(defaultConfig(), "acme/example", beyond)
	clamped.me = "stefanahman"
	if last := clamped.lastPRRowIndex(); clamped.cursor != last {
		t.Errorf("cursor beyond the list: %d, want the last PR row %d", clamped.cursor, last)
	}
}
