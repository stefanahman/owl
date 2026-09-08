// Linear as the issue tracker: the issues assigned to the user, one
// issue by its identifier, a new issue. GraphQL over HTTPS with a
// personal API key — Linear's own recommendation for personal tools —
// and the queries verified against the published schema and the
// workspace's live data.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Tracker is the issue tracker owl reads and writes. Linear is the one
// implementation; the seam is here for a Jira or a GitHub Issues later.
type Tracker interface {
	// Issues lists the open issues assigned to the user, most recently
	// updated first.
	Issues() ([]Issue, error)
	// Issue fetches one by its identifier (BAR-123).
	Issue(key string) (Issue, error)
	// Create files an issue in the configured team, assigned to the
	// user, and returns it.
	Create(title string) (Issue, error)
}

// Issue is what owl shows and acts on.
type Issue struct {
	ID            string    `json:"id"`         // Linear's UUID
	Key           string    `json:"identifier"` // BAR-123
	Title         string    `json:"title"`
	Branch        string    `json:"branchName"`    // the branch Linear wants the work on
	Priority      int       `json:"priority"`      // 0 none, 1 urgent … 4 low
	PriorityLabel string    `json:"priorityLabel"` // as Linear names it
	URL           string    `json:"url"`
	UpdatedAt     time.Time `json:"updatedAt"`
	State         struct {
		Name string `json:"name"` // In Review
		Type string `json:"type"` // triage, backlog, unstarted, started, completed, canceled
	} `json:"state"`
	Team struct {
		Key string `json:"key"` // BAR
	} `json:"team"`
}

// issueFields is what every issue query selects.
const issueFields = `id identifier title branchName priority priorityLabel url updatedAt state { name type } team { key }`

// Linear talks to one workspace with one user's key.
type Linear struct {
	Token secret
	Team  string // key of the team new issues go to
	// Endpoint is Linear's GraphQL endpoint; tests point it elsewhere.
	Endpoint string
	Client   *http.Client
}

// linearEndpoint is Linear's GraphQL endpoint; tests point it at a fake.
var linearEndpoint = "https://api.linear.app/graphql"

// errLinearAuth is a key Linear refused.
var errLinearAuth = errors.New("Linear refused the API key")

// query posts one GraphQL request. A refused key is forgotten and
// read again, once: the cache may hold a key that was revoked.
func (l Linear) query(q string, vars map[string]any, out any) error {
	err := l.post(q, vars, out)
	if errors.Is(err, errLinearAuth) && strings.HasPrefix(l.Token.ref, "op://") {
		if ferr := l.Token.forget(); ferr != nil {
			return ferr
		}
		err = l.post(q, vars, out)
	}
	return err
}

func (l Linear) post(q string, vars map[string]any, out any) error {
	token, err := l.Token.value()
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	body, err := json.Marshal(map[string]any{"query": q, "variables": vars})
	if err != nil {
		return err
	}
	endpoint := l.Endpoint
	if endpoint == "" {
		endpoint = linearEndpoint
	}
	client := l.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("linear: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("%w (HTTP %d)", errLinearAuth, resp.StatusCode)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("linear: HTTP %d: %s", resp.StatusCode, firstLine(string(data)))
	}
	if len(envelope.Errors) > 0 {
		e := envelope.Errors[0]
		if e.Extensions.Code == "AUTHENTICATION_ERROR" {
			return fmt.Errorf("%w: %s", errLinearAuth, e.Message)
		}
		return fmt.Errorf("linear: %s", e.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("linear: HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(envelope.Data, out)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// Issues: the user's open issues — every state but completed and
// canceled — newest change first, up to 100.
func (l Linear) Issues() ([]Issue, error) {
	var r struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []Issue `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	q := `query($first: Int!) { viewer { assignedIssues(first: $first, orderBy: updatedAt, filter: { state: { type: { nin: ["completed", "canceled"] } } }) { nodes { ` + issueFields + ` } } } }`
	if err := l.query(q, map[string]any{"first": 100}, &r); err != nil {
		return nil, err
	}
	return r.Viewer.AssignedIssues.Nodes, nil
}

// Issue looks one up by identifier; Linear's issue(id:) takes the
// identifier as well as the UUID.
func (l Linear) Issue(key string) (Issue, error) {
	var r struct {
		Issue Issue `json:"issue"`
	}
	q := `query($id: String!) { issue(id: $id) { ` + issueFields + ` } }`
	if err := l.query(q, map[string]any{"id": key}, &r); err != nil {
		return Issue{}, err
	}
	return r.Issue, nil
}

// Create files an issue in the team, assigned to the user.
func (l Linear) Create(title string) (Issue, error) {
	if l.Team == "" {
		return Issue{}, errors.New("linear: no team configured (linear.team, the key in BAR-123)")
	}
	var ids struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Teams struct {
			Nodes []struct {
				ID string `json:"id"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	q := `query($key: String!) { viewer { id } teams(filter: { key: { eq: $key } }) { nodes { id } } }`
	if err := l.query(q, map[string]any{"key": l.Team}, &ids); err != nil {
		return Issue{}, err
	}
	if len(ids.Teams.Nodes) == 0 {
		return Issue{}, fmt.Errorf("linear: no team with key %q", l.Team)
	}
	var r struct {
		IssueCreate struct {
			Success bool  `json:"success"`
			Issue   Issue `json:"issue"`
		} `json:"issueCreate"`
	}
	m := `mutation($input: IssueCreateInput!) { issueCreate(input: $input) { success issue { ` + issueFields + ` } } }`
	input := map[string]any{"teamId": ids.Teams.Nodes[0].ID, "title": title, "assigneeId": ids.Viewer.ID}
	if err := l.query(m, map[string]any{"input": input}, &r); err != nil {
		return Issue{}, err
	}
	if !r.IssueCreate.Success {
		return Issue{}, errors.New("linear: issueCreate did not succeed")
	}
	return r.IssueCreate.Issue, nil
}

// newTracker is the tracker for this configuration.
func newTracker(cfg Config, notify func(string)) Tracker {
	return Linear{Token: secret{name: "linear", ref: cfg.Linear.Token, notify: notify}, Team: cfg.Linear.Team}
}
