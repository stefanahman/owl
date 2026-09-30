// On-disk caches for the last-fetched lists. Read at startup so the
// popup shows something within milliseconds instead of waiting on gh
// or Linear (~1-2s). Overwritten atomically whenever a fetch lands.
//
// The PR list's cache is $XDG_CACHE_HOME/owl/<owner>-<name>.json
// (falls back to ~/.cache/owl/…), one file per repo — different repos
// don't stomp on each other. The issue list's is issues.json beside
// them: the issues are the user's, not a repo's.
//
// Each config caches in a folder of its own, named for its file:
// work.yaml's lists in owl/work/, so a work config and a personal one,
// each with its own Linear workspaces and repositories, never show or
// announce each other's rows. config.yaml keeps owl/ itself.
//
// Freshness is user-visible via the "updated Xm ago" indicator in
// the title bar (driven by cache.FetchedAt on load, then m.lastFetched
// once the live fetch lands); `r` spins while a refresh runs.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cacheFile is the on-disk shape. Kept flat and versioned by field
// presence (no explicit version yet — nothing incompatible has shipped).
type cacheFile struct {
	Prs        []PR      `json:"prs"`
	Merged     []PR      `json:"merged"`
	Mine       []PR      `json:"mine"`
	MineMerged []PR      `json:"mineMerged"` // your own, landed inside merged_window
	Me         string    `json:"me"`
	FetchedAt  time.Time `json:"fetchedAt"`
	Cursor     int       `json:"cursor"` // the row the cursor was on at exit; the next start resumes there
}

// issueCacheFile is the issue list's on-disk shape.
type issueCacheFile struct {
	Issues          []Issue   `json:"issues"`
	DoneIssues      []Issue   `json:"doneIssues"`
	CancelledIssues []Issue   `json:"cancelledIssues"`
	IssuePRs        []PR      `json:"issuePrs"`
	FetchedAt       time.Time `json:"fetchedAt"`
	Cursor          int       `json:"cursor"`
}

// projectCacheFile is the project list's on-disk shape. The issues
// ride along because a row counts how many of them are yours.
type projectCacheFile struct {
	Projects     []Project `json:"projects"`
	DoneProjects []Project `json:"doneProjects"`
	Issues       []Issue   `json:"issues"`
	FetchedAt    time.Time `json:"fetchedAt"`
	Cursor       int       `json:"cursor"`
}

// cacheDir is $XDG_CACHE_HOME/owl, else ~/.cache/owl, followed by the
// config's own folder; "" when neither base can be found.
func cacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "owl", configCacheName())
}

// configCacheName is the folder the config in use caches in: its file's
// name without the extension, found after following links, so a link to
// a config (config.yaml pointing at work.yaml) shares that config's
// cache rather than keeping a second copy. config.yaml itself, the one
// config most setups have, caches in the top folder: "".
func configCacheName() string {
	path, err := configPath()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	switch name {
	case "config", "", ".", "..":
		return ""
	}
	return name
}

// cachePath returns the per-repo cache file path. `repo` is owner/name;
// slashes flatten to hyphens for a safe filename. Empty repo → empty
// path (caller should skip cache).
func cachePath(repo string) string {
	if repo == "" || cacheDir() == "" {
		return ""
	}
	return filepath.Join(cacheDir(), strings.ReplaceAll(repo, "/", "-")+".json")
}

// issueCachePath is the issue list's cache file.
func issueCachePath() string {
	if cacheDir() == "" {
		return ""
	}
	return filepath.Join(cacheDir(), "issues.json")
}

// loadCache reads and parses the cache file for the given repo. Any
// error (missing file, malformed JSON, unreadable) returns nil — the
// caller falls back to an empty initial state and waits for the live
// fetch.
func loadCache(repo string) *cacheFile {
	var c cacheFile
	if !readJSON(cachePath(repo), &c) {
		return nil
	}
	return &c
}

// loadIssueCache is loadCache for the issue list.
func loadIssueCache() *issueCacheFile {
	var c issueCacheFile
	if !readJSON(issueCachePath(), &c) {
		return nil
	}
	return &c
}

// projectCachePath is the project list's cache file. Like the issue
// list's it is not per repo: the projects are the user's.
func projectCachePath() string {
	if cacheDir() == "" {
		return ""
	}
	return filepath.Join(cacheDir(), "projects.json")
}

// loadProjectCache is loadCache for the project list.
func loadProjectCache() *projectCacheFile {
	var c projectCacheFile
	if !readJSON(projectCachePath(), &c) {
		return nil
	}
	return &c
}

// readJSON parses the file into v; false when there is nothing usable.
func readJSON(path string, v any) bool {
	if path == "" {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// saveCache writes the PR list's cache; saveIssueCache the issue
// list's.
func saveCache(repo string, c cacheFile) { writeJSON(cachePath(repo), c) }

func saveIssueCache(c issueCacheFile) { writeJSON(issueCachePath(), c) }

func saveProjectCache(c projectCacheFile) { writeJSON(projectCachePath(), c) }

// writeJSON writes the cache atomically (temp file + rename) so a
// crash mid-write never leaves a truncated JSON on disk. All errors
// are swallowed — cache is a best-effort optimization, never critical.
func writeJSON(p string, c any) {
	if p == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	// A unique temp file: two owl instances writing at once must not
	// rename each other's half-written file into place.
	f, err := os.CreateTemp(filepath.Dir(p), ".cache-*")
	if err != nil {
		return
	}
	if _, err := f.Write(b); err != nil || f.Close() != nil {
		os.Remove(f.Name())
		return
	}
	if err := os.Rename(f.Name(), p); err != nil {
		os.Remove(f.Name())
	}
}
