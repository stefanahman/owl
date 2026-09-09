// The issue scope: the issues assigned to the user, from the tracker,
// and new ones. The list is a table for now; the TUI, the worktrees
// and the feature windows follow.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// runIssue dispatches `owl issue [command]`.
func runIssue(cfg Config, args []string, out io.Writer) error {
	tracker := newTracker(cfg, func(text string) {
		fmt.Fprintln(os.Stderr, text)
		newWindows(cfg, features).Notify(text)
	})
	if len(args) == 0 {
		return listIssues(tracker, out)
	}
	switch args[0] {
	case "new":
		return hoot(tracker, args[1:], out)
	case "open":
		return runIssueOpen(cfg, tracker, args[1:], out, true)
	case "start":
		return runIssueOpen(cfg, tracker, args[1:], out, false)
	case "close":
		return runIssueClose(cfg, args[1:], out)
	}
	return usageError("issue: unknown command " + args[0])
}

// listIssues prints the user's open issues, newest change first. Only
// the open ones: the TUI's Done section is a glance at what just
// closed, a table piped into something else wants one answer.
func listIssues(tracker Tracker, out io.Writer) error {
	issues, err := tracker.Issues()
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		fmt.Fprintln(out, "no open issues assigned to you")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tPRIO\tAGE\tSTATE\tPROJECT\tTITLE")
	for _, is := range issues {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", is.Key, priorityMark(is.Priority), relativeAge(is.UpdatedAt.Format(time.RFC3339)), is.State.Name, trim(is.Project.Name, 24), trim(is.Title, 70))
	}
	return w.Flush()
}

// priorityMark is Linear's priority in one glyph: !!! urgent, !! high,
// ! medium, - low, and nothing for none.
func priorityMark(p int) string {
	switch p {
	case 1:
		return "!!!"
	case 2:
		return "!!"
	case 3:
		return "!"
	case 4:
		return "-"
	}
	return ""
}

// hoot files an issue with the words given as its title and prints
// where it landed.
func hoot(tracker Tracker, words []string, out io.Writer) error {
	title := strings.TrimSpace(strings.Join(words, " "))
	if title == "" {
		return usageError("hoot: a title is required: owl hoot \"what needs doing\"")
	}
	is, err := tracker.Create(title)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%s  %s\n%s\n", is.Key, is.Title, is.URL)
	return nil
}
