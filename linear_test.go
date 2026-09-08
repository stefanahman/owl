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
)

// fakeLinear serves Linear's GraphQL shapes: the queries the tracker
// sends, answered from canned data, with the key checked.
type fakeLinear struct {
	t       *testing.T
	key     string
	queries []string
	created []string
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
		return map[string]any{"id": "uuid-" + key, "identifier": key, "title": title, "branchName": branch, "priority": 2, "priorityLabel": "High", "url": "https://linear.app/x/issue/" + key, "updatedAt": "2026-09-08T13:55:43.474Z", "state": map[string]string{"name": state, "type": stype}, "team": map[string]string{"key": "BAR"}}
	}
	var data any
	switch {
	case strings.Contains(req.Query, "assignedIssues"):
		if !strings.Contains(req.Query, `nin: ["completed", "canceled"]`) || !strings.Contains(req.Query, "orderBy: updatedAt") {
			f.t.Errorf("issues query lacks the filter or the order: %s", req.Query)
		}
		data = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": []any{
			issue("BAR-4159", "Company fuzzy match", "bar-4159-company-fuzzy-match", "In Review", "started"),
			issue("BAR-4160", "Per-tenant override", "bar-4160-per-tenant-override", "Todo", "unstarted"),
		}}}}
	case strings.Contains(req.Query, "issue(id: $id)"):
		key, _ := req.Variables["id"].(string)
		if key != "BAR-4159" {
			data = nil
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"errors":[{"message":"Entity not found: Issue - Could not find referenced Issue.","extensions":{"code":"INVALID_INPUT"}}]}`)
			return
		}
		data = map[string]any{"issue": issue("BAR-4159", "Company fuzzy match", "bar-4159-company-fuzzy-match", "In Review", "started")}
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
	_, srv := newFakeLinear(t, "lin_key")
	l := Linear{Token: secret{name: "linear", ref: "lin_key"}, Team: "BAR", Endpoint: srv.URL}
	issues, err := l.Issues()
	if err != nil || len(issues) != 2 || issues[0].Key != "BAR-4159" || issues[0].Branch != "bar-4159-company-fuzzy-match" || issues[0].State.Type != "started" || issues[0].PriorityLabel != "High" || issues[0].UpdatedAt.IsZero() {
		t.Fatalf("Issues() = %+v, %v", issues, err)
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
