package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// openWorkspace runs `owl <kind> open <id> [--prompt TEXT]` and reports
// when it ends. The child gets its own session (Setsid): owl usually
// runs inside a tmux popup, and with on_open: quit the popup closes
// the moment the child starts — it must finish on its own, and it
// does (see runChild for where its failure goes then).
func (m model) openWorkspace(noun, id, prompt string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"open", id}
		if prompt != "" {
			args = append(args, "--prompt", prompt)
		}
		return openedMsg{id, m.runSelf(noun, args...)}
	}
}

// startWorkspace runs `owl <kind> start <id> [--prompt TEXT]`: the
// workspace comes up, or gets the prompt, and the list stays — for
// starting several one after another, and for f.
func (m model) startWorkspace(noun, id, prompt string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"start", id}
		if prompt != "" {
			args = append(args, "--prompt", prompt)
		}
		return openedMsg{id, m.runSelf(noun, args...)}
	}
}

// closeWorkspace runs `owl <kind> close <id>`; the TUI refreshes its
// overlay when it succeeds. The agent's conversation survives on
// disk, so Enter / f afterwards resume it.
func (m model) closeWorkspace(noun, id string) tea.Cmd {
	return func() tea.Msg {
		return closedMsg{id, m.runSelf(noun, "close", id)}
	}
}

// onOpen is what the TUI does once an open starts: the setting, or
// with auto what fits the multiplexer — quit under tmux, where the
// list is a popup that closes at once; stay under herdr and cmux,
// where the list has a workspace of its own and quitting would leave
// a dead shell there.
func (m model) onOpen() string {
	if m.cfg.OnOpen != "auto" {
		return m.cfg.OnOpen
	}
	if newWindows(m.cfg, m.sc).Kind() == "tmux" {
		return "quit"
	}
	return "stay"
}

// launch starts an open, start or close child for a row in the
// background. The list stays usable meanwhile; a second key on the
// same row is refused until the child reports. An open (arrive) with
// on_open: quit ends the TUI at once — the popup closes, the child
// finishes behind it.
func (m model) launch(id, label string, cmd tea.Cmd, arrive bool) (tea.Model, tea.Cmd) {
	if running, ok := m.inflight[id]; ok {
		m.notice = fmt.Errorf("still %s", running)
		return m, nil
	}
	m.inflight[id] = label
	if arrive && m.onOpen() == "quit" {
		m.farewell = label
		return m, tea.Batch(cmd, tea.Quit)
	}
	return m, tea.Batch(cmd, m.spinner.Tick)
}

// runChild runs this binary with args in its own session and returns
// nil, or its failure with the child's stderr in the message. env is
// the multiplexer's ChildEnv, so the child can notify the right way.
//
// The child outlives the TUI with on_open: quit, and a Go program
// writing to a broken pipe on stdout or stderr is killed by SIGPIPE —
// so its stdout is discarded rather than piped, and its failure also
// goes to the multiplexer before it is printed (see exitOn).
func runChild(env []string, args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate owl binary: %w", err)
	}
	// A test binary would run its whole suite as `owl pr open`, and that
	// suite would do it again. Tests inject runSelf; this catches the
	// one that forgets.
	if strings.HasSuffix(self, ".test") {
		return fmt.Errorf("%s is a test binary, not owl", self)
	}
	cmd := exec.Command(self, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("owl %s: %s", childCommand(args), msg)
	}
	return nil
}

// childCommand names the child's command line for a message with the
// prompt text elided: a prompt runs to a paragraph, and the reason for
// the failure comes after it, off the edge of the notice line.
func childCommand(args []string) string {
	shown := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--prompt" && i+1 < len(args):
			shown = append(shown, "--prompt …")
			i++
		case strings.HasPrefix(args[i], "--prompt="):
			shown = append(shown, "--prompt=…")
		default:
			shown = append(shown, args[i])
		}
	}
	return strings.Join(shown, " ")
}

// openPRInBrowser opens the PR's page. pr.URL comes from the API, so
// this is right on GitHub Enterprise too; it is empty only while a
// pre-url cache file is showing and the first fetch hasn't landed.
func (m model) openPRInBrowser(pr *PR) tea.Cmd {
	return func() tea.Msg {
		if pr.URL == "" {
			return noticeMsg{fmt.Errorf("PR #%d: URL not loaded yet", pr.Number)}
		}
		return openURL(m.cfg.OpenCmd, pr.URL)
	}
}

// pressNoun is the noun a binding's child runs under: the selected
// row's, since a binding acts on what the cursor is on.
func (m model) pressNoun() string {
	row, _ := m.selectedRow()
	return row.noun()
}

// bindings is the list's own: the PR list's or the issue list's.
func (m model) bindings() []Binding {
	switch m.kind {
	case "issue":
		return m.cfg.Bindings.Issue
	case "project":
		return nil // no per-row prompts until a project has a workspace
	}
	// Your own PR answers to different keys than someone else's: the
	// review pane's f re-reads the feedback you are giving, the mine
	// pane's the review you were given.
	if m.mineFocus {
		return m.cfg.Bindings.Mine
	}
	return m.cfg.Bindings.PR
}

// press does what a binding says on the selected row. A prompt goes
// through `start --prompt`, like s with words: a fresh workspace
// starts with it, a running Claude receives it, a blocked one refuses
// it — and the list stays. With `when: conversation` the key applies
// only to work that has been started: a workspace, or a conversation
// Claude kept on disk after the window went. A URL opens. A pattern
// that matches nothing makes the key a no-op.
func (m model) press(b Binding) (tea.Model, tea.Cmd) {
	var id, label, text string
	var ok bool
	row, _ := m.selectedRow()
	if b.When == whenConversation && !m.started(row) {
		return m, nil
	}
	switch {
	case m.selectedPR() != nil:
		pr := m.selectedPR()
		id, label = strconv.Itoa(pr.Number), fmt.Sprintf("#%d", pr.Number)
		text, ok = b.forPR(m.repo, pr)
	case m.selectedIssue() != nil:
		is := m.selectedIssue()
		id, label = is.Key, is.Key
		text, ok = b.forIssue(m.repo, is)
	default:
		return m, nil
	}
	if !ok {
		return m, nil
	}
	if b.URL != "" {
		return m, func() tea.Msg { return openURL(m.cfg.OpenCmd, text) }
	}
	return m.launch(id, fmt.Sprintf("%s on %s…", b.Name, label), m.startWorkspace(m.pressNoun(), id, text), false)
}

// switchClient moves the user's client to the review container, whose
// current window `open` has just selected.
func switchClient(mx windows) tea.Cmd {
	return func() tea.Msg {
		if err := mx.SwitchClient(); err != nil {
			return noticeMsg{err}
		}
		return nil
	}
}

// openURL hands a URL to `open_cmd` (split on whitespace, URL
// appended), defaulting to the platform opener.
func openURL(openCmd, url string) tea.Msg {
	argv := strings.Fields(openCmd)
	if len(argv) == 0 {
		if runtime.GOOS == "darwin" {
			argv = []string{"open"}
		} else {
			argv = []string{"xdg-open"}
		}
	}
	argv = append(argv, url)
	if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
		return noticeMsg{fmt.Errorf("%s %s: %w", argv[0], url, err)}
	}
	return nil
}

// yankPRURL copies the PR's URL to the clipboard through OSC 52 — the
// terminal does the copying, so it works over SSH and inside tmux
// (`set-clipboard on`) without pbcopy or xclip.
func yankPRURL(pr *PR) tea.Cmd {
	if pr.URL == "" {
		return func() tea.Msg { return noticeMsg{fmt.Errorf("PR #%d: URL not loaded yet", pr.Number)} }
	}
	return tea.SetClipboard(pr.URL)
}
