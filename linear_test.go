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
	t              *testing.T
	key            string
	queries        []string
	created        []string
	doneSince      string // the completedAt bound the last Done() sent
	cancelledSince string // the canceledAt bound the last Cancelled() sent

	projectLookups    int    // project(id:) calls, to prove open takes the cheap path
	doneProjectsSince string // the completedAt bound the last DoneProjects() sent
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
	inProject := func(n map[string]any, slug, name string) map[string]any {
		n["project"] = map[string]string{"id": "uuid-" + slug, "name": name}
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
			nodes = []any{inProject(n, "5b16b5f4", "Endpoint Validation")}
		case stype["eq"] == "canceled":
			// The bound matters more here than anywhere: Linear takes an
			// `or:` of two windowed branches and then applies neither, so
			// a query without its own canceledAt would quietly return
			// everything ever cancelled.
			f.cancelledSince, _ = filter["canceledAt"].(map[string]any)["gte"].(string)
			if f.cancelledSince == "" {
				f.t.Errorf("the cancelled query carries no canceledAt bound: %v", filter)
			}
			n := issue("BAR-4287", "Drop the shadow validation", "bar-4287-drop-shadow-validation", "Cancelled", "canceled")
			n["canceledAt"] = "2026-09-09T12:00:00.000Z"
			nodes = []any{n}
		case len(stype["nin"].([]any)) == 2:
			// Two pages of one: the client must follow the cursor.
			nodes, page = []any{inProject(issue("BAR-4159", "Company fuzzy match", "bar-4159-company-fuzzy-match", "In Review", "started"), "a76d38ca8527", "Sequential Capture redesign")}, map[string]any{"hasNextPage": true, "endCursor": "cursor-1"}
			if after, _ := req.Variables["after"].(string); after == "cursor-1" {
				nodes, page = []any{issue("BAR-4160", "Per-tenant override", "bar-4160-per-tenant-override", "Todo", "unstarted")}, map[string]any{"hasNextPage": false, "endCursor": nil}
			} else if after != "" {
				f.t.Errorf("issues query with an unknown cursor %q", after)
			}
		default:
			f.t.Errorf("assignedIssues with an unexpected filter: %v", filter)
		}
		data = map[string]any{"viewer": map[string]any{"assignedIssues": map[string]any{"nodes": nodes, "pageInfo": page}}}
	case strings.Contains(req.Query, "project(id: $id)"):
		// One lookup, not the whole filtered list: this is the path
		// `owl project open <id>` takes.
		f.projectLookups++
		switch id, _ := req.Variables["id"].(string); id {
		case "a76d38ca8527", "uuid-a76d38ca8527":
			data = map[string]any{"project": map[string]any{
				"id": "uuid-a76d38ca8527", "name": "Sequential Capture redesign", "slugId": "a76d38ca8527",
				"url": "https://linear.app/x/project/a76d38ca8527", "progress": 0.62, "scope": 127,
				"updatedAt": "2026-09-08T13:55:43.474Z",
				"status":    map[string]string{"name": "In Progress", "type": "started"},
				"lead":      map[string]string{"name": "Stefan Åhman"},
				"projectMilestones": map[string]any{"nodes": []any{
					map[string]any{"id": "ms-1", "name": "M3.5 — Downstream compatibility", "progress": 0.8},
				}},
			}}
		default:
			// Linear answers a name fragment with a null project, not an
			// error; the caller falls back to the list.
			data = map[string]any{"project": nil}
		}
	case strings.Contains(req.Query, "projects(first:"):
		// Lead or member, and nothing about issues: Linear's project-level
		// `issues: { some: … }` does not conjoin per issue, so owl asks
		// only what Linear can answer and derives the rest itself.
		for _, want := range []string{"lead: { isMe: { eq: true } }", "members: { isMe: { eq: true } }"} {
			if !strings.Contains(req.Query, want) {
				f.t.Errorf("projects query lacks %q: %s", want, req.Query)
			}
		}
		if strings.Contains(req.Query, "assignee:") {
			f.t.Errorf("the projects query still asks Linear about issues: %s", req.Query)
		}
		project := func(name, slug, state, stype string, progress float64, scope int, milestones []any) map[string]any {
			return map[string]any{
				"id": "uuid-" + slug, "name": name, "slugId": slug,
				"url":      "https://linear.app/x/project/" + slug,
				"progress": progress, "scope": scope, "updatedAt": "2026-09-08T13:55:43.474Z",
				"status":            map[string]string{"name": state, "type": stype},
				"lead":              map[string]string{"name": "Stefan Åhman"},
				"projectMilestones": map[string]any{"nodes": milestones},
			}
		}
		// `$since`, not "completedAt": the field is selected by both
		// queries, so only the variable tells the two apart.
		if strings.Contains(req.Query, "$since") {
			// The Done window. Its bound is recorded for the assertion.
			f.doneProjectsSince, _ = req.Variables["since"].(string)
			done := project("Rule-driven activity grouping", "f6824f4cbe9e", "Completed", "completed", 1, 16, nil)
			done["completedAt"] = time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
			data = map[string]any{"projects": map[string]any{
				"nodes":    []any{done},
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
			}}
			break
		}
		// Two pages of one, both the user's: the client must follow the
		// cursor.
		milestone := func(name string, progress float64) map[string]any {
			return map[string]any{"id": "ms-" + name, "name": name, "progress": progress}
		}
		nodes, page := []any{project("Sequential Capture redesign", "a76d38ca8527", "In Progress", "started", 0.62, 127,
			[]any{milestone("M3.5 — Downstream compatibility", 0.8), milestone("M5 — Output evaluation", 0.1)})},
			map[string]any{"hasNextPage": true, "endCursor": "proj-1"}
		if after, _ := req.Variables["after"].(string); after == "proj-1" {
			nodes, page = []any{project("Raw-data JSON ingest", "eb528db32d42", "Backlog", "backlog", 0.12, 12, nil)},
				map[string]any{"hasNextPage": false, "endCursor": nil}
		} else if after != "" {
			f.t.Errorf("projects query with an unknown cursor %q", after)
		}
		data = map[string]any{"projects": map[string]any{"nodes": nodes, "pageInfo": page}}
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
	if issues[0].Project.Name != "Sequential Capture redesign" || issues[0].Project.ID == "" || issues[1].Project.Name != "" {
		t.Errorf("projects = %q, %q", issues[0].Project.Name, issues[1].Project.Name)
	}
	// An open issue has no completedAt: Linear sends null.
	if !issues[0].CompletedAt.IsZero() {
		t.Errorf("an open issue completed at %v", issues[0].CompletedAt)
	}
	// Cancelled is its own request with its own bound. One `or:` beside
	// the done filter is a query Linear accepts and then answers by
	// state type alone, ignoring both windows.
	cancelled, err := l.Cancelled(time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC))
	if err != nil || len(cancelled) != 1 || cancelled[0].Key != "BAR-4287" || cancelled[0].State.Type != "canceled" || cancelled[0].CanceledAt.IsZero() {
		t.Fatalf("Cancelled() = %+v, %v", cancelled, err)
	}
	if f.cancelledSince != "2026-09-07T12:00:00Z" {
		t.Errorf("cancelled bound = %q", f.cancelledSince)
	}

	since := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	done, err := l.Done(since)
	if err != nil || len(done) != 1 || done[0].Key != "BAR-4286" || done[0].State.Type != "completed" || done[0].CompletedAt.IsZero() {
		t.Fatalf("Done() = %+v, %v", done, err)
	}
	if f.doneSince != "2026-09-08T12:00:00Z" {
		t.Errorf("Done() asked for completedAt >= %q", f.doneSince)
	}
	// Two pages of one: the client must follow the cursor. Nothing is
	// derived — the list is what Linear says the user leads or belongs
	// to, and membership is maintained in Linear.
	projects, err := l.Projects()
	if err != nil || len(projects) != 2 {
		t.Fatalf("Projects() = %+v, %v", projects, err)
	}
	p := projects[0]
	if p.Name != "Sequential Capture redesign" || p.SlugID != "a76d38ca8527" || p.State.Type != "started" || p.Lead.Name != "Stefan Åhman" {
		t.Errorf("first project = %+v", p)
	}
	if projects[1].Name != "Raw-data JSON ingest" || len(projects[1].Milestones.Nodes) != 0 {
		t.Errorf("second project = %+v", projects[1])
	}
	// scope is the project's issue count, not the length of a capped
	// connection; progress is Linear's own fraction.
	if p.Scope != 127 || p.Progress != 0.62 || len(p.Milestones.Nodes) != 2 || p.Milestones.Nodes[0].Progress != 0.8 {
		t.Errorf("project numbers = scope %d, progress %v, milestones %+v", p.Scope, p.Progress, p.Milestones.Nodes)
	}

	// The Done window is a week, and its bound reaches Linear.
	doneProjects, err := l.DoneProjects(time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC))
	if err != nil || len(doneProjects) != 1 || doneProjects[0].State.Type != "completed" || doneProjects[0].CompletedAt.IsZero() {
		t.Fatalf("DoneProjects() = %+v, %v", doneProjects, err)
	}
	if f.doneProjectsSince != "2026-09-02T12:00:00Z" {
		t.Errorf("DoneProjects() asked for completedAt >= %q", f.doneProjectsSince)
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
