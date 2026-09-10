package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGHOnPath puts a gh of the given script on PATH for the test.
func fakeGHOnPath(t *testing.T, script string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return bin
}

// TestOpenPRsAnswersWhoYouAre: the login rides along in the search's
// own response. It used to be a `gh api user` of its own — half a
// second of round trip on every refresh, and again on every
// `owl pr --check`, for one string that never changes.
func TestOpenPRsAnswersWhoYouAre(t *testing.T) {
	fakeGHOnPath(t, `#!/bin/sh
printf '%s' '{"data":{"viewer":{"login":"stefanahman"},"search":{"nodes":[{"number":7,"title":"a change","author":{"login":"alice"},"reviews":{"nodes":[]}}]}}}'
`)
	prs, me, err := openPRs("acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if me != "stefanahman" {
		t.Errorf("me = %q", me)
	}
	if len(prs) != 1 || prs[0].Number != 7 || prs[0].Author.Login != "alice" {
		t.Errorf("prs = %+v", prs)
	}
}

// A failing gh must not report an empty login as a fact: `--check`
// reads it to decide whose court a PR is in.
func TestOpenPRsFailsRatherThanGuessing(t *testing.T) {
	fakeGHOnPath(t, "#!/bin/sh\necho 'gh: nope' >&2\nexit 1\n")
	if _, me, err := openPRs("acme/app"); err == nil || me != "" {
		t.Errorf("openPRs = %q, %v; want an error and no login", me, err)
	}
	if _, _, err := openPRs("acme/app"); err != nil && !strings.Contains(err.Error(), "gh api graphql") {
		t.Errorf("error does not say what failed: %v", err)
	}
}
