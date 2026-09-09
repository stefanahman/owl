// The project scope: the projects the user works in. Off a terminal
// `owl project` prints a table; in one it is the third list.
package main

import (
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

// runProject dispatches `owl project [command]`.
func runProject(cfg Config, args []string, out io.Writer) error {
	tracker := newTracker(cfg, func(text string) {
		fmt.Fprintln(os.Stderr, text)
		newWindows(cfg, features).Notify(text)
	})
	if len(args) == 0 {
		return listProjects(tracker, out)
	}
	switch args[0] {
	case "open":
		return runProjectOpen(cfg, tracker, args[1:], out, true)
	case "start":
		return runProjectOpen(cfg, tracker, args[1:], out, false)
	case "close":
		return runProjectClose(cfg, tracker, args[1:], out)
	}
	return usageError("project: unknown command " + args[0])
}

// listProjects prints the open projects the user works in, newest
// change first.
func listProjects(tracker Tracker, out io.Writer) error {
	projects, err := tracker.Projects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Fprintln(out, "no open projects you work in")
		return nil
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROGRESS\tISSUES\tMS\tSTATE\tPROJECT")
	for _, p := range projects {
		milestones := ""
		if n := len(p.Milestones.Nodes); n > 0 {
			milestones = fmt.Sprintf("%d", n)
		}
		fmt.Fprintf(w, "%.0f%%\t%d\t%s\t%s\t%s\n", p.Progress*100, p.Scope, milestones, p.State.Name, trim(p.Name, 70))
	}
	return w.Flush()
}
