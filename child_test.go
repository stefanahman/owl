package main

import "testing"

// A failed child is reported by its command line; the prompt, a
// paragraph long, is elided so the reason after it stays on screen.
func TestChildCommandElidesPrompt(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"pr", "start", "4278", "--prompt", "Please carefully check the feedback since your last review"}, "pr start 4278 --prompt …"},
		{[]string{"pr", "open", "42", "--prompt=fix the tests"}, "pr open 42 --prompt=…"},
		{[]string{"pr", "close", "42"}, "pr close 42"},
		{[]string{"pr", "open", "42", "--prompt"}, "pr open 42 --prompt"},
	} {
		if got := childCommand(tc.args); got != tc.want {
			t.Errorf("childCommand(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
