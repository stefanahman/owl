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

// TestCacheKeepsTheRepo: the cache is the list's first paint, and a
// row read back without its repository is a row whose number means
// nothing — #3 is a different pull request in each of them.
func TestCacheKeepsTheRepo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)
	saveCache("acme/app", cacheFile{
		Prs:  []PR{{Number: 1, Title: "theirs", Repo: "acme/app"}},
		Mine: []PR{{Number: 3, Title: "yours", Repo: "acme/other"}},
	})
	got := loadCache("acme/app")
	if got == nil {
		t.Fatal("nothing came back")
	}
	if len(got.Prs) != 1 || got.Prs[0].Repo != "acme/app" {
		t.Errorf("review row came back as %+v", got.Prs)
	}
	if len(got.Mine) != 1 || got.Mine[0].Repo != "acme/other" {
		t.Errorf("mine row came back as %+v", got.Mine)
	}
}

// useConfig points owl at a config file of that name in dir, as
// $OWL_CONFIG does, creating the file.
func useConfig(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWL_CONFIG", path)
	return path
}

// TestEachConfigCachesApart: two configs — a work one and a personal
// one, each with its own Linear workspaces and repositories — keep
// their lists, and so --check's baseline, in folders of their own.
func TestEachConfigCachesApart(t *testing.T) {
	cache, configs := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	useConfig(t, configs, "work.yaml")
	saveIssueCache(issueCacheFile{Issues: []Issue{{Key: "WRK-1"}}})
	saveCache("acme/app", cacheFile{Me: "work"})
	if want := filepath.Join(cache, "owl", "work", "issues.json"); issueCachePath() != want {
		t.Errorf("issue cache %s, want %s", issueCachePath(), want)
	}

	useConfig(t, configs, "personal.yaml")
	if got := loadIssueCache(); got != nil {
		t.Errorf("personal config read the work config's issues: %+v", got.Issues)
	}
	if got := loadCache("acme/app"); got != nil {
		t.Errorf("personal config read the work config's PR cache (me %q)", got.Me)
	}

	useConfig(t, configs, "work.yaml")
	if got := loadIssueCache(); got == nil || len(got.Issues) != 1 || got.Issues[0].Key != "WRK-1" {
		t.Errorf("work config lost its own issues: %+v", got)
	}
}

// TestALinkedConfigSharesItsCache: a config reached through a link —
// config.yaml pointing at whichever config is in use — caches with the
// file it points at, so the link and the file never keep two copies.
func TestALinkedConfigSharesItsCache(t *testing.T) {
	cache, configs := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	target := useConfig(t, configs, "work.yaml")
	link := filepath.Join(configs, "config.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OWL_CONFIG", link)
	if want := filepath.Join(cache, "owl", "work", "projects.json"); projectCachePath() != want {
		t.Errorf("project cache %s, want %s", projectCachePath(), want)
	}
}

// TestConfigYamlKeepsTheTopFolder: the one config most setups have
// caches where it always has.
func TestConfigYamlKeepsTheTopFolder(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	useConfig(t, t.TempDir(), "config.yaml")
	if want := filepath.Join(cache, "owl", "issues.json"); issueCachePath() != want {
		t.Errorf("issue cache %s, want %s", issueCachePath(), want)
	}
	if want := filepath.Join(cache, "owl", "acme-app.json"); cachePath("acme/app") != want {
		t.Errorf("PR cache %s, want %s", cachePath("acme/app"), want)
	}
}

// spanning is a config whose PR list spans owners, as pr.owners does.
func spanning() Config {
	cfg := defaultConfig()
	cfg.PR.Owners = []string{"@me", "acme"}
	return cfg
}

// TestHereKeepsItsOwnCache: the list --here shows (this repo alone) is
// not the one that spans the owners, so it must not overwrite that
// cache, which is also owl pr --check's baseline: a baseline holding
// one repo's rows would announce every other repo's as arrived.
func TestHereKeepsItsOwnCache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := spanning()

	all := newModel(cfg, "acme/app", nil)
	all.prs = fixturePRs()
	all.persistCache()

	here := newModel(cfg, "acme/app", nil)
	here.here = true
	here.prs = fixturePRs()[:1]
	here.persistCache()

	if got := loadCache(prCacheKey(cfg.PR, "acme/app", false)); got == nil || len(got.Prs) != len(fixturePRs()) {
		t.Fatalf("the list across the owners holds %v, want its own %d rows", got, len(fixturePRs()))
	}
	if got := loadCache(prCacheKey(cfg.PR, "acme/app", true)); got == nil || len(got.Prs) != 1 {
		t.Fatalf("the --here list holds %v, want its 1 row", got)
	}
}

// TestASpanningListIsOneCache: the list across the owners is the same
// from any repo, so it caches once, not once per repo it was opened in.
func TestASpanningListIsOneCache(t *testing.T) {
	cfg := spanning()
	if a, b := prCacheKey(cfg.PR, "acme/app", false), prCacheKey(cfg.PR, "acme/other", false); a != b {
		t.Errorf("spanning list keyed %q in one repo and %q in another", a, b)
	}
	if prCacheKey(cfg.PR, "", false) == "" {
		t.Error("a spanning list needs no repo to be cached")
	}
	plain := defaultConfig()
	if got := prCacheKey(plain.PR, "acme/app", false); got != "acme/app" {
		t.Errorf("without owners the list is the repo's: keyed %q, want acme/app", got)
	}
}
