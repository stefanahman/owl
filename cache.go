// On-disk cache for the last-fetched PR list, keyed by repo. Read at
// startup so the popup shows something within milliseconds instead of
// waiting on gh (~1-2s). Overwritten atomically whenever prsMsg lands.
//
// Cache location: $XDG_CACHE_HOME/owl/<owner>-<name>.json (falls
// back to ~/.cache/owl/…). One file per repo — different repos
// don't stomp on each other.
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
	Prs       []PR      `json:"prs"`
	Merged    []PR      `json:"merged"`
	Me        string    `json:"me"`
	FetchedAt time.Time `json:"fetchedAt"`
	Cursor    int       `json:"cursor"` // the row the cursor was on at exit; the next start resumes there
}

// cachePath returns the per-repo cache file path. `repo` is owner/name;
// slashes flatten to hyphens for a safe filename. Empty repo → empty
// path (caller should skip cache).
func cachePath(repo string) string {
	if repo == "" {
		return ""
	}
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".cache")
	}
	safe := strings.ReplaceAll(repo, "/", "-")
	return filepath.Join(base, "owl", safe+".json")
}

// loadCache reads and parses the cache file for the given repo. Any
// error (missing file, malformed JSON, unreadable) returns nil — the
// caller falls back to an empty initial state and waits for the live
// fetch.
func loadCache(repo string) *cacheFile {
	p := cachePath(repo)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var c cacheFile
	if err := json.Unmarshal(b, &c); err != nil {
		return nil
	}
	return &c
}

// saveCache writes the cache atomically (temp file + rename) so a
// crash mid-write never leaves a truncated JSON on disk. All errors
// are swallowed — cache is a best-effort optimization, never critical.
func saveCache(repo string, c cacheFile) {
	p := cachePath(repo)
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
