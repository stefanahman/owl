// The command line: what owl is, how it is spelled, and the dispatch
// from a noun to the thing that runs it. The only part of owl that is
// not the terminal UI, which is why it sits beside the model rather
// than inside it.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/term"
)

// version is set by the release build (-ldflags "-X main.version=…");
// `go install …@vX.Y.Z` builds report the module version instead.
var version = ""

func versionString() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// about is what bare owl says: what it is, before what it takes.
const about = `owl — the pull requests waiting for your review, the issues waiting for
your hands, and the projects they belong to, one keystroke from any
terminal.

Each becomes a workspace when you want it: a git worktree, a window in
your multiplexer — tmux, herdr or cmux — and Claude Code inside it, on
the review, the feature or the project. owl stores nothing of its own:
the branch carries the ticket, the pull request carries the review, the
window carries the agent, and the lists read all of it back from GitHub,
Linear and the multiplexer. Four skills give the agent its manners —
review never posts without you, feature never pushes without you,
project plans and dispatches but writes no code itself, dependabot fixes
on the bot's branch. What no ticket names yet, you hoot.
`

const usage = `usage: owl [--config FILE] [--mux tmux|herdr|cmux] [<noun> [command]]
       owl                              this introduction
       owl pr                           the PR list (run inside a git repo, or with default_repo set)
       owl pr open <N> [--prompt TEXT]  open (or focus) the review of PR N
       owl pr start <N> [--prompt TEXT] the same without going there: no window selection, no after_open
       owl pr close [--force] [<N>]     remove PR N's worktree, branch and window; --force discards uncommitted changes
       owl pr --check                   say what has arrived in your court since owl last looked, for a scheduler
       owl issue                        the issues assigned to you, from Linear
       owl issue open <KEY> [--prompt TEXT]  open (or focus) the feature workspace of issue KEY
       owl issue start <KEY> [--prompt TEXT] the same without going there
       owl issue close [--force] [<KEY>]     remove the feature's worktree, local branch and window
       owl issue new <title…>           file an issue in linear.team, assigned to you
       owl issue --project <id|name>    a project's open issues by milestone, whoever they belong to
       owl project                      the projects you work in, from Linear
       owl project open <id> [--prompt TEXT] open (or focus) the project's conversation
       owl project start <id> [--prompt TEXT] the same without going there
       owl project close [--force] <id> remove the project's worktree and window
       owl hoot <title…>                the same, from the owl
       owl config init | path
       owl --version`

// usageError is a bad invocation: the message is printed with the
// usage text and the process exits 64 (EX_USAGE).
type usageError string

func (e usageError) Error() string { return string(e) }

func main() {
	args, err := globalOptions(os.Args[1:])
	exitOn(err)
	// Bare owl says what it is; the lists are behind their nouns, so a
	// verb never has to guess which kind of thing an id names.
	if len(args) == 0 {
		fmt.Print(about)
		fmt.Println()
		fmt.Println(usage)
		return
	}
	// The noun is the scope. A verb without one is refused with the
	// form it takes.
	if len(args) > 0 {
		switch args[0] {
		case "config":
			exitOn(runConfig(args[1:], os.Stdout))
			return
		case "--version", "version":
			fmt.Println("owl", versionString())
			return
		case "--help", "-h", "help":
			fmt.Println(usage)
			return
		case "pr":
			args = args[1:]
			if len(args) > 0 {
				switch args[0] {
				case "open", "start", "close", "--check", "--here":
				default:
					exitOn(usageError("pr: unknown command " + args[0]))
				}
			}
		case "issue":
			if len(args) > 1 && !strings.HasPrefix(args[1], "--project") {
				switch args[1] {
				case "open", "start", "close", "new":
				default:
					exitOn(usageError("issue: unknown command " + args[1]))
				}
			}
		case "project":
			if len(args) > 1 {
				switch args[1] {
				case "open", "start", "close":
				default:
					exitOn(usageError("project: unknown command " + args[1]))
				}
			}
		case "hoot":
		case "open", "start", "close":
			exitOn(usageError(args[0] + " is a pr command: owl pr " + args[0]))
		default:
			exitOn(usageError("unknown command " + args[0]))
		}
	}

	cfg, err := loadConfig()
	exitOn(err)
	if muxOverride != "" {
		cfg.Mux = muxOverride
	}
	exitOn(enterDefaultRepo(cfg.DefaultRepo))
	switch {
	case len(args) > 0 && args[0] == "issue":
		if len(args) == 1 && term.IsTerminal(os.Stdout.Fd()) {
			exitOn(newWindows(cfg, features).Ping())
			tracker := newTracker(cfg, func(text string) {
				fmt.Fprintln(os.Stderr, text)
				newWindows(cfg, features).Notify(text)
			})
			runTUI(initialIssueModel(cfg, tracker))
			return
		}
		exitOn(runIssue(cfg, args[1:], os.Stdout))
		return
	case len(args) > 0 && args[0] == "project":
		if len(args) == 1 && term.IsTerminal(os.Stdout.Fd()) {
			exitOn(newWindows(cfg, projects).Ping())
			tracker := newTracker(cfg, func(text string) {
				fmt.Fprintln(os.Stderr, text)
				newWindows(cfg, features).Notify(text)
			})
			runTUI(initialProjectModel(cfg, tracker))
			return
		}
		exitOn(runProject(cfg, args[1:], os.Stdout))
		return
	case len(args) > 0 && args[0] == "hoot":
		exitOn(runIssue(cfg, append([]string{"new"}, args[1:]...), os.Stdout))
		return
	case len(args) == 0:
		// Before the list: states read from a tainted multiplexer never
		// change, and the failure would surface on Enter, an hour in.
		exitOn(newWindows(cfg, reviews).Ping())
		runTUI(initialModel(cfg))
	case args[0] == "--here":
		// This repo, whatever pr.owners spans.
		exitOn(newWindows(cfg, reviews).Ping())
		m := initialModel(cfg)
		m.here = true
		runTUI(m)
	case args[0] == "--check":
		err = runCheck(cfg, os.Stdout)
	case args[0] == "open":
		err = runOpen(cfg, args[1:], os.Stdout, true)
	case args[0] == "start":
		err = runOpen(cfg, args[1:], os.Stdout, false)
	case args[0] == "close":
		err = runClose(cfg, args[1:], os.Stdout)
	}
	exitOn(err)
}

// runTUI runs a list until it quits, keeps its cursor for the next
// start, and prints what an open left for the terminal behind it.
//
// The context is the watch's: cmux's event stream is a child process
// held open for as long as the list is watching, and cancelling here
// kills it the moment the list ends rather than leaving it to notice
// the broken pipe on its next heartbeat.
func runTUI(m model) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	m.ctx = ctx
	final, err := tea.NewProgram(m).Run()
	exitOn(err)
	fm := final.(model)
	fm.persistCache() // the cursor row, for the next start
	if fm.farewell != "" {
		fmt.Println(fm.farewell)
	}
}

// muxOverride is the --mux flag, when given: the multiplexer to use
// whatever the config says.
var muxOverride string

// globalOptions takes --config FILE and --mux KIND off the front of
// the arguments — they apply to every command — and returns the rest.
func globalOptions(args []string) ([]string, error) {
	for len(args) > 0 {
		name, value, joined := strings.Cut(args[0], "=")
		if name != "--config" && name != "--mux" {
			break
		}
		if !joined {
			if len(args) < 2 {
				return nil, usageError(name + " needs a value")
			}
			value, args = args[1], args[1:]
		}
		args = args[1:]
		switch name {
		case "--config":
			configOverride = value
		case "--mux":
			switch value {
			case "tmux", "herdr", "cmux":
				muxOverride = value
			default:
				return nil, usageError("--mux must be tmux, herdr or cmux, got " + value)
			}
		}
	}
	return args, nil
}

// globalArgs is what a child owl needs in front of its command to
// see the same --config and --mux as this process: a child parses its
// own arguments.
func globalArgs() []string {
	var args []string
	if configOverride != "" {
		args = append(args, "--config", configOverride)
	}
	if muxOverride != "" {
		args = append(args, "--mux", muxOverride)
	}
	return args
}

// enterDefaultRepo changes into `default_repo` when the working
// directory isn't inside a git repo, so owl can be launched from
// anywhere (a hotkey, a popup) and still act on the configured repo.
func enterDefaultRepo(defaultRepo string) error {
	if defaultRepo == "" || exec.Command("git", "rev-parse", "--git-dir").Run() == nil {
		return nil
	}
	if err := os.Chdir(defaultRepo); err != nil {
		return fmt.Errorf("default_repo: %w", err)
	}
	return nil
}

// exitOn prints err and exits: 64 for a usage error (with the usage
// text), 2 when `close` found nothing to do, 1 otherwise.
func exitOn(err error) {
	if err == nil {
		return
	}
	// Started by the TUI, which may already have quit (on_open: quit):
	// the failure goes to the multiplexer the popup was in — before
	// stderr, which may be a broken pipe by now and would end the
	// process.
	if mx, ok := windowsByKind(os.Getenv("OWL_MUX")); ok {
		mx.Notify(err.Error())
	}
	fmt.Fprintf(os.Stderr, "owl: %v\n", err)
	var ue usageError
	var nothing nothingToCloseError
	switch {
	case errors.As(err, &ue):
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(64)
	case errors.As(err, &nothing):
		os.Exit(2)
	}
	os.Exit(1)
}
