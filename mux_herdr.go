// herdr as the multiplexer: one herdr workspace per PR, its root pane
// running the agent. herdr detects the agent itself and reports what it
// is doing, so no companion is needed for the state. Everything goes
// over herdr's socket: one newline-delimited JSON request per
// connection, its response read back.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

type herdrMux struct{ socket string }

// newHerdrMux finds the socket: the configured one, else the one herdr
// gives its panes, else the default session's.
func newHerdrMux(cfg HerdrConfig) herdrMux {
	sock := cfg.Socket
	if sock == "" {
		sock = os.Getenv("HERDR_SOCKET_PATH")
	}
	if sock == "" {
		home, _ := os.UserHomeDir()
		sock = filepath.Join(home, ".config", "herdr", "herdr.sock")
	}
	return herdrMux{socket: sock}
}

// herdrError is an error herdr answered with; the code is what a
// caller can act on (agent_blocked, not_found, …).
type herdrError struct{ code, message string }

func (e *herdrError) Error() string { return "herdr: " + e.message + " (" + e.code + ")" }

// call sends one request and returns its result.
func (h herdrMux) call(method string, params any) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", h.socket, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("herdr: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	id := "pr-owl/" + method
	req, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("herdr %s: %w", method, err)
	}
	rd := bufio.NewReader(conn)
	for {
		line, err := rd.ReadBytes('\n')
		var resp struct {
			ID     string          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &resp) == nil && resp.ID == id {
			if resp.Error != nil {
				return nil, &herdrError{resp.Error.Code, resp.Error.Message}
			}
			return resp.Result, nil
		}
		if err != nil {
			return nil, fmt.Errorf("herdr %s: no answer: %w", method, err)
		}
	}
}

func (herdrMux) Kind() string { return "herdr" }

// Prepare has nothing to create — reviews are workspaces, and herdr
// has no container above them — but it makes sure herdr is there.
func (h herdrMux) Prepare(string) error {
	_, err := h.call("ping", struct{}{})
	return err
}

type herdrWorkspace struct {
	ID     string `json:"workspace_id"`
	Label  string `json:"label"`
	Status string `json:"agent_status"`
}

func (h herdrMux) workspaces() ([]herdrWorkspace, error) {
	raw, err := h.call("workspace.list", struct{}{})
	if err != nil {
		return nil, err
	}
	var r struct {
		Workspaces []herdrWorkspace `json:"workspaces"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("herdr workspace.list: %w", err)
	}
	return r.Workspaces, nil
}

// Windows are the workspaces named like reviews; herdr holds the
// user's other work too.
func (h herdrMux) Windows() []string {
	ws, err := h.workspaces()
	if err != nil {
		return nil
	}
	var names []string
	for _, w := range ws {
		if prNumberOf(w.Label) > 0 {
			names = append(names, w.Label)
		}
	}
	return names
}

func (h herdrMux) workspaceID(name string) (string, error) {
	ws, err := h.workspaces()
	if err != nil {
		return "", err
	}
	for _, w := range ws {
		if w.Label == name {
			return w.ID, nil
		}
	}
	return "", fmt.Errorf("herdr: no workspace %q", name)
}

// rootPane is the pane the agent runs in: the workspace's first.
func (h herdrMux) rootPane(name string) (string, error) {
	id, err := h.workspaceID(name)
	if err != nil {
		return "", err
	}
	raw, err := h.call("pane.list", map[string]any{"workspace_id": id})
	if err != nil {
		return "", err
	}
	var r struct {
		Panes []struct {
			ID string `json:"pane_id"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Panes) == 0 {
		return "", fmt.Errorf("herdr: workspace %q has no pane", name)
	}
	return r.Panes[0].ID, nil
}

// Open creates the workspace without taking the focus — Select does
// that when the user is arriving — and types the start line into its
// root pane. herdr's own `agent start` is not used: it fails when the
// agent shows a dialog while starting, and Claude Code's trust prompt
// is one.
func (h herdrMux) Open(name, dir, startLine string) error {
	raw, err := h.call("workspace.create", map[string]any{"cwd": dir, "label": name, "focus": false})
	if err != nil {
		return err
	}
	var r struct {
		RootPane struct {
			ID string `json:"pane_id"`
		} `json:"root_pane"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || r.RootPane.ID == "" {
		return fmt.Errorf("herdr workspace.create: no root pane in the answer")
	}
	return h.sendLine(r.RootPane.ID, startLine)
}

// sendLine types a line and Enter as one submission.
func (h herdrMux) sendLine(paneID, line string) error {
	_, err := h.call("pane.send_input", map[string]any{"pane_id": paneID, "text": line, "keys": []string{"enter"}})
	return err
}

// States maps herdr's words onto pr-owl's: the same four, and unknown
// (no agent detected in the workspace) is "".
func (h herdrMux) States() map[string]string {
	ws, err := h.workspaces()
	if err != nil {
		return nil
	}
	states := map[string]string{}
	for _, w := range ws {
		if prNumberOf(w.Label) == 0 {
			continue
		}
		switch w.Status {
		case agentWorking, agentBlocked, agentDone, agentIdle:
			states[w.Label] = w.Status
		default:
			states[w.Label] = ""
		}
	}
	return states
}

// AtShell asks for the pane's foreground process: herdr's detected
// state flickers through idle mid-turn, so an exited agent is read
// from the process, never from the state.
func (h herdrMux) AtShell(name string) bool {
	pane, err := h.rootPane(name)
	if err != nil {
		return false
	}
	raw, err := h.call("pane.process_info", map[string]any{"pane_id": pane})
	if err != nil {
		return false
	}
	var r struct {
		Info struct {
			Foreground []struct {
				Name string `json:"name"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(raw, &r); err != nil || len(r.Info.Foreground) == 0 {
		return false
	}
	return isShell(r.Info.Foreground[0].Name)
}

// Prompt hands the text to herdr's agent.prompt, which refuses on its
// own while the agent is blocked, so keystrokes never answer a dialog.
func (h herdrMux) Prompt(name, text string) error {
	pane, err := h.rootPane(name)
	if err != nil {
		return err
	}
	_, err = h.call("agent.prompt", map[string]any{"target": pane, "text": text})
	return err
}

func (h herdrMux) Run(name, line string) error {
	pane, err := h.rootPane(name)
	if err != nil {
		return err
	}
	return h.sendLine(pane, line)
}

// Select focuses the workspace; every client attached to the session
// follows, which is why SwitchClient has nothing left to do.
func (h herdrMux) Select(name string) error {
	id, err := h.workspaceID(name)
	if err != nil {
		return err
	}
	_, err = h.call("workspace.focus", map[string]any{"workspace_id": id})
	return err
}

func (herdrMux) SwitchClient() error { return nil }

func (h herdrMux) Close(name string) error {
	id, err := h.workspaceID(name)
	if err != nil {
		return err
	}
	_, err = h.call("workspace.close", map[string]any{"workspace_id": id})
	return err
}

// Current is the workspace of the pane pr-owl runs in, which herdr
// names in the environment.
func (h herdrMux) Current() (string, bool) {
	id := os.Getenv("HERDR_WORKSPACE_ID")
	if id == "" {
		return "", false
	}
	ws, err := h.workspaces()
	if err != nil {
		return "", false
	}
	for _, w := range ws {
		if w.ID == id {
			return w.Label, true
		}
	}
	return "", false
}

func (herdrMux) Describe(name string) string { return name }

var herdrSessionSocket = regexp.MustCompile(`/sessions/([^/]+)/herdr\.sock$`)

func (h herdrMux) AttachHint() string {
	if os.Getenv("HERDR_ENV") == "1" {
		return ""
	}
	if m := herdrSessionSocket.FindStringSubmatch(h.socket); m != nil {
		return "herdr --session " + m[1]
	}
	return "herdr"
}

func (h herdrMux) Notify(text string) {
	_, _ = h.call("notification.show", map[string]any{"title": "pr-owl", "body": text})
}

func (herdrMux) Env(name string) map[string]string {
	return map[string]string{"PR_OWL_SESSION": os.Getenv("HERDR_SESSION"), "PR_OWL_WINDOW": name}
}
