package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeLinear serves Linear's GraphQL shapes: the queries the tracker
// sends, answered from canned data, with the key checked.
type fakeLinear struct {
	t         *testing.T
	key       string
	queries   []string
	created   []string
	doneSince string // the completedAt bound the last Done() sent
}

func (f *fakeLinear) handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != f.key {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errors":[{"message":"Authentication required, not authenticated","extensions":{"code":"AUTHENTICATION_ERROR"}}]}`)
		return
	}
	var req struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("bad request body: %v", err)
	}
	f.queries = append(f.queries, req.Query)
	issue := func(key, title, branch, state, stype string) map[string]any {
		return map[string]any{"id": "uuid-" + key, "identifier": key, "title": title, "branchName": branch, "priority": 2, "priorityLabel": "High", "url": "https://linear.app/x/issue/" + key, "updatedAt": "2026-09-08T13:55:43.474Z", "state": map[string]string{"name": state, "type": stype}, "project": nil, "team": map[string]string{"key": "BAR"}}
	}
	// A project, as Linear sends it on the issues that belong to one.
	inProject := func(n map[string]any, name string) map[string]any {
		n["project"] = map[string]string{"name": name}
		return n
	}
	var data any
	switch {
	case strings.Contains(req.Query, "assignedIssues"):
		if !strings.Contains(req.Query, "orderBy: updatedAt") || !strings.Contains(req.Query, "pageInfo { hasNextPage endCursor }") {
			f.t.Errorf("issues query lacks the order or the page info: %s", req.Query)
		}
		// The filter travels as a variable; which one says whether this
		// is the open list or the done window.
		filter, _ := req.Variables["filter"].(map[string]any)
		state, _ := filter["state"].(map[string]any)
		stype, _ := state["type"].(map[string]any)
		var nodes []any
		page := map[string]any{"hasNextPage": false, "endCursor": nil}
		switch {
		case stype["eq"] == "completed":
			f.doneSince, _ = filter["completedAt"].(map[string]any)["gte"].(string)
			if f.doneSince == "" {
				f.t.Errorf("the done query carries no completedAt bound: %v", filter)
			}
			n := issue("BAR-4286", "Open update-activity fields", "bar-4286-open-update-activity", "Done", "completed")
			n["completedAt"] = "2026-09-09T09:00:00.000Z"
			nodes = []any{inProject(n, "Endpoint Validation")}
		case len(stype["nin"].([]any)) == 2:
			// Two pages of one: the client must follow the cursor.
			nodes, page = []any{inProject(issue("BAR-4159", "Company fuzzy match", "bar-4159-company-fuzzy-match", "In Review", "started"), "Sequential Capture")}, map[string]any{"hasNextPage": true, "endCursor": "cursor-1"}
			if after, _ := req.Variables["after"].(string); after == "cursor-1" {
				nodes, page = []any{issue("BAR-4160", "Per-tenant override", "bar-4160-per-tenant-override", "Todo", "unstarted")}, map[string]any{"hasNextPage": false, "endCursor": nil}
			} else if after != "" {
				f.t.Errorf("issues query with an unknown cursor %q", after)
			}
		default:
			f.t.Errorf("assignedIssues with an unexpected filter: %v", filter)
		}
		data = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": nodes, "pageInfo": page}}}
	case strings.Contains(req.Query, "issue(id: $id)"):
		switch key, _ := req.Variables["id"].(string); key {
		case "BAR-4159":
			data = map[string]any{"issue": issue("BAR-4159", "Company fuzzy match", "bar-4159-company-fuzzy-match", "In Review", "started")}
		case "BAR-4160":
			data = map[string]any{"issue": issue("BAR-4160", "Per-tenant override", "bar-4160-per-tenant-override", "Todo", "unstarted")}
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"errors":[{"message":"Entity not found: Issue - Could not find referenced Issue.","extensions":{"code":"INVALID_INPUT"}}]}`)
			return
		}
	case strings.Contains(req.Query, "teams(filter"):
		key, _ := req.Variables["key"].(string)
		nodes := []any{}
		if key == "BAR" {
			nodes = append(nodes, map[string]string{"id": "team-bar"})
		}
		data = map[string]any{"viewer": map[string]string{"id": "me-uuid"}, "teams": map[string]any{"nodes": nodes}}
	case strings.Contains(req.Query, "issueCreate"):
		input, _ := req.Variables["input"].(map[string]any)
		if input["teamId"] != "team-bar" || input["assigneeId"] != "me-uuid" {
			f.t.Errorf("issueCreate input = %v", input)
		}
		title, _ := input["title"].(string)
		f.created = append(f.created, title)
		data = map[string]any{"issueCreate": map[string]any{"success": true, "issue": issue("BAR-9001", title, "bar-9001-"+strings.ToLower(strings.ReplaceAll(title, " ", "-")), "Todo", "unstarted")}}
	default:
		f.t.Errorf("unexpected query: %s", req.Query)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func newFakeLinear(t *testing.T, key string) (*fakeLinear, *httptest.Server) {
	f := &fakeLinear{t: t, key: key}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	return f, srv
}

func TestLinearIssuesIssueAndCreate(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f, srv := newFakeLinear(t, "lin_key")
	l := Linear{Token: secret{name: "linear", ref: "lin_key"}, Team: "BAR", Endpoint: srv.URL}
	issues, err := l.Issues()
	if err != nil || len(issues) != 2 || issues[0].Key != "BAR-4159" || issues[0].Branch != "bar-4159-company-fuzzy-match" || issues[0].State.Type != "started" || issues[0].PriorityLabel != "High" || issues[0].UpdatedAt.IsZero() {
		t.Fatalf("Issues() = %+v, %v", issues, err)
	}
	if pages := len(f.queries); pages != 2 || issues[1].Key != "BAR-4160" {
		t.Errorf("Issues() read %d pages for two; second issue %q", pages, issues[1].Key)
	}
	// The project comes back where there is one, and an issue outside a
	// project reads as empty rather than failing to parse.
	if issues[0].Project.Name != "Sequential Capture" || issues[1].Project.Name != "" {
		t.Errorf("projects = %q, %q", issues[0].Project.Name, issues[1].Project.Name)
	}
	// An open issue has no completedAt: Linear sends null.
	if !issues[0].CompletedAt.IsZero() {
		t.Errorf("an open issue completed at %v", issues[0].CompletedAt)
	}
	since := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	done, err := l.Done(since)
	if err != nil || len(done) != 1 || done[0].Key != "BAR-4286" || done[0].State.Type != "completed" || done[0].CompletedAt.IsZero() {
		t.Fatalf("Done() = %+v, %v", done, err)
	}
	if f.doneSince != "2026-09-08T12:00:00Z" {
		t.Errorf("Done() asked for completedAt >= %q", f.doneSince)
	}
	one, err := l.Issue("BAR-4159")
	if err != nil || one.Title != "Company fuzzy match" || one.Team.Key != "BAR" {
		t.Errorf("Issue() = %+v, %v", one, err)
	}
	if _, err := l.Issue("BAR-1"); err == nil || !strings.Contains(err.Error(), "Could not find") {
		t.Errorf("unknown issue: %v", err)
	}
	made, err := l.Create("Fix the owl")
	if err != nil || made.Key != "BAR-9001" || made.Branch != "bar-9001-fix-the-owl" {
		t.Errorf("Create() = %+v, %v", made, err)
	}
	if _, err := (Linear{Token: l.Token, Endpoint: srv.URL}).Create("x"); err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Errorf("create without a team: %v", err)
	}
	if _, err := (Linear{Token: l.Token, Team: "NOPE", Endpoint: srv.URL}).Create("x"); err == nil || !strings.Contains(err.Error(), `no team with key "NOPE"`) {
		t.Errorf("create with an unknown team: %v", err)
	}
}

func TestLinearForgetsARefusedKeyOnce(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	calls := fakeOp(t, "lin_key")
	_, srv := newFakeLinear(t, "lin_key")
	// A stale key in the cache: Linear refuses it, the cache goes, the
	// key is read again, the call succeeds.
	path := filepath.Join(state, "owl", "linear.token")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("revoked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Linear{Token: secret{name: "linear", ref: "op://Vault/Linear/credential"}, Endpoint: srv.URL}
	issues, err := l.Issues()
	if err != nil || len(issues) != 2 {
		t.Fatalf("Issues() after a refused key = %v, %v", issues, err)
	}
	if calls() != 1 {
		t.Errorf("op read called %d times, want once", calls())
	}
	if v, _ := readCached(path); v != "lin_key" {
		t.Errorf("cache holds %q, want the new key", v)
	}
	// Refused again with a fresh key: the error, no loop.
	_, bad := newFakeLinear(t, "other")
	l.Endpoint = bad.URL
	if _, err := l.Issues(); err == nil || !strings.Contains(err.Error(), "refused the API key") || calls() != 2 {
		t.Errorf("still refused: %v, op calls %d", err, calls())
	}
}
