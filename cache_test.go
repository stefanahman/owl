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
