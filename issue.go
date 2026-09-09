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
	// `--project X` lists a project's issues, whoever they belong to —
	// what the project view drills into, and what a project's agent
	// asks for when it wants more than its own desk.
	if id, ok, err := projectFlag(args); err != nil {
		return err
	} else if ok {
		p, err := findProject(tracker, id)
		if err != nil {
			return err
		}
		issues, err := tracker.ProjectIssues(p.ID)
		if err != nil {
			return err
		}
		return listProjectIssues(p, issues, out)
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

// projectFlag takes `--project X` or `--project=X` off the arguments.
// ok is false when the flag is not there at all.
func projectFlag(args []string) (id string, ok bool, err error) {
	for i, a := range args {
		switch {
		case a == "--project":
			if i+1 == len(args) {
				return "", false, usageError("issue --project: a project is required")
			}
			return args[i+1], true, nil
		case strings.HasPrefix(a, "--project="):
			return strings.TrimPrefix(a, "--project="), true, nil
		}
	}
	return "", false, nil
}

// listProjectIssues prints a project's open issues grouped by
// milestone, in the project's own order, with the unmilestoned last.
// The assignee column is blank for yours, so the gaps are your queue.
func listProjectIssues(p Project, issues []Issue, out io.Writer) error {
	if len(issues) == 0 {
		fmt.Fprintf(out, "%s: no open issues\n", p.Name)
		return nil
	}
	mine := 0
	for _, is := range issues {
		if is.Assignee.IsMe {
			mine++
		}
	}
	fmt.Fprintf(out, "%s · %.0f%% · %d open, %d yours\n\n", p.Name, p.Progress*100, len(issues), mine)
	// Fixed widths rather than a tabwriter: a milestone heading carries
	// no tabs, which ends the tabwriter's column block, so every
	// section would align only against itself.
	for _, sec := range milestoneSections(issues) {
		fmt.Fprintln(out, sec.title)
		for _, is := range sec.issues {
			who := ""
			if !is.Assignee.IsMe {
				who = firstWord(is.Assignee.Name)
			}
			fmt.Fprintf(out, "  %-9s %-3s %3s  %-11s %-8s %s\n",
				is.Key, priorityMark(is.Priority), relativeAge(is.UpdatedAt.Format(time.RFC3339)),
				trim(is.State.Name, 11), trim(who, 8), trim(is.Title, 58))
		}
	}
	return nil
}

// firstWord is a person's given name: enough to tell whose issue it is
// in a column that cannot hold more.
func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
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
