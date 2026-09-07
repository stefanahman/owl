package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		"/home/me/src/app/.worktrees.local/pr-7": "-home-me-src-app--worktrees-local-pr-7",
		// Claude Code replaces every non-alphanumeric character, not
		// only the path separators and dots.
		"/Users/me/my_app/pr 7@v2": "-Users-me-my-app-pr-7-v2",
	}
	for in, want := range cases {
		if got := encodeProjectPath(in); got != want {
			t.Errorf("encodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPriorWorkspaceName(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	projects, err := claudeProjectsDir()
	if err != nil {
		t.Fatal(err)
	}
	transcript := func(cwd string) {
		dir := filepath.Join(projects, encodeProjectPath(cwd))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	transcript("/r/.wt/pr-7-old-title")
	transcript("/r/.wt/pr-70-other")
	transcript("/elsewhere/.wt/pr-8") // another repo's PR 8
	if err := os.MkdirAll(filepath.Join(projects, encodeProjectPath("/r/.wt/pr-9")), 0o755); err != nil {
		t.Fatal(err) // a project dir with no transcript
	}
	for n, want := range map[int]string{7: "pr-7-old-title", 70: "pr-70-other", 8: "", 9: "", 10: ""} {
		if got := priorWorkspaceName("/r", ".wt", n); got != want {
			t.Errorf("priorWorkspaceName(pr %d) = %q, want %q", n, got, want)
		}
	}
	if hasPriorConversation("", ".wt", 7) {
		t.Error("outside a repo there is nothing to resume")
	}
}
