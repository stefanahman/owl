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
	prs, me, err := openPRs("repo:acme/app", "is:open")
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
	if _, me, err := openPRs("repo:acme/app", "is:open"); err == nil || me != "" {
		t.Errorf("openPRs = %q, %v; want an error and no login", me, err)
	}
	if _, _, err := openPRs("repo:acme/app", "is:open"); err != nil && !strings.Contains(err.Error(), "gh api graphql") {
		t.Errorf("error does not say what failed: %v", err)
	}
}

// TestPRScope covers what the PR searches are scoped by: the repo you
// are standing in, or every owner the list spans.
func TestPRScope(t *testing.T) {
	none := PRConfig{}
	if got := none.Scope("acme/app", false); got != "repo:acme/app" {
		t.Errorf("no owners = %q, want the one repo", got)
	}
	if got := none.Scope("", false); got != "" {
		t.Errorf("no owners and no repo = %q, want nothing to search", got)
	}

	// user: and not org: for all of them — GitHub treats the two as
	// synonyms for the account that owns a repo, so a list of owners
	// need not say which are people and which are organisations.
	two := PRConfig{Owners: []string{"@me", "norrbrunn"}}
	if got := two.Scope("acme/app", false); got != "(user:@me OR user:norrbrunn)" {
		t.Errorf("owners = %q", got)
	}
	// --here narrows to this repo even where owners would span more.
	if got := two.Scope("acme/app", true); got != "repo:acme/app" {
		t.Errorf("--here = %q, want the one repo", got)
	}
	if !two.Spans() || none.Spans() {
		t.Error("Spans does not say which of them covers more than one repo")
	}
}

// TestScopeORsOwners pins the one spelling GitHub's code search reads
// as a union. `user:a user:b` is ANDed there — a pull request has one
// owner, so it answers nothing — while the REST search API ORs the same
// string. A query verified against REST and run against GraphQL is the
// way this went wrong once already.
func TestScopeORsOwners(t *testing.T) {
	one := PRConfig{Owners: []string{"@me"}}
	if got := one.Scope("acme/app", false); got != "(user:@me)" {
		t.Errorf("one owner = %q", got)
	}
	three := PRConfig{Owners: []string{"@me", "norrbrunn", "acme"}}
	want := "(user:@me OR user:norrbrunn OR user:acme)"
	if got := three.Scope("", false); got != want {
		t.Errorf("three owners = %q, want %q", got, want)
	}
	if strings.Contains(three.Scope("", false), "user:@me user:") {
		t.Error("the owners are ANDed, which GitHub answers with nothing")
	}
	// Owners that are all empty fall back to the repo rather than
	// producing "()", which matches nothing at all.
	blank := PRConfig{Owners: []string{"", ""}}
	if got := blank.Scope("acme/app", false); got != "repo:acme/app" {
		t.Errorf("blank owners = %q, want the repo", got)
	}
	if got := blank.Scope("", false); got != "" {
		t.Errorf("blank owners and no repo = %q, want nothing to search", got)
	}
}

// TestOpeningElsewhereIsRefused: a pull request number means nothing
// without its repository — #3 is a different one in each — and `open`
// fetches pull/<N>/head from the repo owl is in.
func TestOpeningElsewhereIsRefused(t *testing.T) {
	m := newModel(defaultConfig(), "acme/app", nil)
	here := visibleRow{pr: &PR{Number: 3, Repo: "acme/app"}}
	if got := m.elsewhere(here); got != "" {
		t.Errorf("a row from this repo reads as elsewhere: %q", got)
	}
	away := visibleRow{pr: &PR{Number: 3, Repo: "acme/other"}}
	if got := m.elsewhere(away); got != "acme/other" {
		t.Errorf("a row from another repo = %q, want acme/other", got)
	}
	// Case is GitHub's business, not a difference.
	mixed := visibleRow{pr: &PR{Number: 3, Repo: "ACME/App"}}
	if got := m.elsewhere(mixed); got != "" {
		t.Errorf("case alone made a row foreign: %q", got)
	}
	// Nothing known: no repo on the row, or owl outside a repo.
	if got := m.elsewhere(visibleRow{pr: &PR{Number: 3}}); got != "" {
		t.Errorf("a row with no repo = %q", got)
	}
}
