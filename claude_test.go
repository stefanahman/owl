package main

import "testing"

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
