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
	// Done lists the issues assigned to the user completed since the
	// given time — what just left the open list, still worth seeing.
	Done(since time.Time) ([]Issue, error)
	// Cancelled lists the issues assigned to the user cancelled since
	// the given time. A separate call rather than one `or:` beside Done:
	// Linear accepts an `or:` of two branches and then applies neither
	// branch's date bound, so one query would have returned every issue
	// ever cancelled.
	Cancelled(since time.Time) ([]Issue, error)
	// Projects lists the open projects the user works in.
	Projects() ([]Project, error)
	// DoneProjects lists the user's projects completed since the given
	// time — what just left the open list, still worth seeing.
	DoneProjects(since time.Time) ([]Project, error)
	// Project fetches one by Linear's slug id or UUID.
	Project(id string) (Project, error)
	// ProjectIssues lists a project's open issues, whoever they belong
	// to — the project's own view, not the user's slice of it.
	ProjectIssues(id string) ([]Issue, error)
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
	// CompletedAt is when the issue was closed; the zero time while it
	// is open. Linear sends null for an open issue, which unmarshals to
	// the zero value.
	CompletedAt time.Time `json:"completedAt"`
	// CanceledAt is when the issue was cancelled — Linear's spelling,
	// one l — and the zero time otherwise.
	CanceledAt time.Time `json:"canceledAt"`
	State      struct {
		Name string `json:"name"` // In Review
		Type string `json:"type"` // triage, backlog, unstarted, started, completed, canceled
	} `json:"state"`
	// Project is the piece of work the issue belongs to; empty for an
	// issue filed outside one. The id is what the project list counts
	// by — two projects may be named alike.
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	// Milestone is the step of the project this issue belongs to, and
	// what the project's own view groups by. Empty for an issue in no
	// milestone, which is common: a project's issues are milestoned as
	// its plan firms up, not when they are filed. SortOrder is the
	// milestone's own, so the sections come out in the project's order
	// rather than alphabetically.
	Milestone struct {
		ID        string  `json:"id"`
		Name      string  `json:"name"`
		SortOrder float64 `json:"sortOrder"`
	} `json:"projectMilestone"`
	// Assignee is whose issue it is. IsMe is Linear's own answer, which
	// beats comparing names: the project's view shows everyone's.
	Assignee struct {
		Name string `json:"name"`
		IsMe bool   `json:"isMe"`
	} `json:"assignee"`
	Team struct {
		Key string `json:"key"` // BAR
	} `json:"team"`
}

// Milestone is a step inside a project. In a Linear workspace this is
// where the specs and the progress actually live, so it is what the
// project view groups by.
type Milestone struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Progress float64 `json:"progress"` // 0..1
}

// Project is the piece of work an issue belongs to.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SlugID    string    `json:"slugId"` // Linear's short id, the tail of its URL
	URL       string    `json:"url"`
	Progress  float64   `json:"progress"` // 0..1, Linear's own
	Scope     int       `json:"scope"`    // issues in it, whoever they belong to
	Priority  int       `json:"priority"` // 1 urgent … 4 low, 0 none — as an issue's
	UpdatedAt time.Time `json:"updatedAt"`
	// TargetDate is Linear's TimelessDate, `2026-11-16` — a day with no
	// time in it — and "" when the project has no date set.
	TargetDate string `json:"targetDate"`
	// CompletedAt is when the project was closed; the zero time while
	// it is open, since Linear sends null.
	CompletedAt time.Time `json:"completedAt"`
	State       struct {
		Name string `json:"name"` // In Progress
		Type string `json:"type"` // backlog, planned, started, paused, completed, canceled
	} `json:"status"`
	Lead struct {
		Name string `json:"name"`
	} `json:"lead"`
	// Initiatives is the layer above the project. Linear allows several;
	// the row has space for one, and one is what nearly every project
	// has.
	Initiatives struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"initiatives"`
	Milestones struct {
		Nodes []Milestone `json:"nodes"`
	} `json:"projectMilestones"`
}

// Initiative is the initiative the project belongs to, or "".
func (p Project) Initiative() string {
	if len(p.Initiatives.Nodes) == 0 {
		return ""
	}
	return p.Initiatives.Nodes[0].Name
}

// projectFields is what the project query selects. `scope` is the
// issue count: the issues connection caps at fifty, so counting its
// nodes would report 50 for a project of 915.
const projectFields = `id name slugId url progress scope priority targetDate updatedAt completedAt status { name type } lead { name } initiatives(first: 1) { nodes { name } } projectMilestones(first: 50) { nodes { id name progress } }`

// issueFields is what every issue query selects.
const issueFields = `id identifier title branchName priority priorityLabel url updatedAt completedAt canceledAt state { name type } project { id name } projectMilestone { id name sortOrder } assignee { name isMe } team { key }`

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
		// Linear's latency varies by a factor of fifty on the same query:
		// the project list has been measured at 380ms and, minutes
		// later, at 18.3s. A tight timeout turns that into a failure
		// where waiting would have worked, and every fetch here is
		// either async behind a cache or a one-shot command.
		client = &http.Client{Timeout: 60 * time.Second}
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

// assignedQuery walks the issues assigned to the user under a filter,
// newest change first. The filter travels as a variable rather than
// baked into the string: one query serves both the open list and the
// recently done, and Linear type-checks the shape.
const assignedQuery = `query($first: Int!, $after: String, $filter: IssueFilter) { viewer { assignedIssues(first: $first, after: $after, orderBy: updatedAt, filter: $filter) { nodes { ` + issueFields + ` } pageInfo { hasNextPage endCursor } } } }`

// assigned reads every page of the filter's answer, 100 at a time.
// There is no cap: the list shows the user's work, and a cap would
// silently hide the tail of it.
func (l Linear) assigned(filter map[string]any) ([]Issue, error) {
	var all []Issue
	var after *string
	for {
		var r struct {
			Viewer struct {
				AssignedIssues struct {
					Nodes    []Issue `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"assignedIssues"`
			} `json:"viewer"`
		}
		if err := l.query(assignedQuery, map[string]any{"first": 100, "after": after, "filter": filter}, &r); err != nil {
			return nil, err
		}
		page := r.Viewer.AssignedIssues
		all = append(all, page.Nodes...)
		if !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == "" {
			return all, nil
		}
		after = &page.PageInfo.EndCursor
	}
}

// Issues: the user's open issues — every state but completed and
// canceled — newest change first.
func (l Linear) Issues() ([]Issue, error) {
	return l.assigned(map[string]any{
		"state": map[string]any{"type": map[string]any{"nin": []string{"completed", "canceled"}}},
	})
}

// Done: the issues completed since the given time. Canceled ones stay
// out — cancelling is not finishing, and seeing it again is noise.
func (l Linear) Done(since time.Time) ([]Issue, error) {
	return l.assigned(map[string]any{
		"state":       map[string]any{"type": map[string]any{"eq": "completed"}},
		"completedAt": map[string]any{"gte": since.UTC().Format(time.RFC3339)},
	})
}

// Cancelled: the issues cancelled since the given time. Cancelling is
// not finishing — which is why these have a section of their own
// rather than joining the Done one — but it is still something that
// happened to your work, and often not by your hand.
//
// Its own request, and deliberately. `or: [{completed, completedAt},
// {canceled, canceledAt}]` is a filter Linear accepts and then answers
// by state type alone, ignoring both date bounds: verified against the
// live API, where it returned issues cancelled two months back.
func (l Linear) Cancelled(since time.Time) ([]Issue, error) {
	return l.assigned(map[string]any{
		"state":      map[string]any{"type": map[string]any{"eq": "canceled"}},
		"canceledAt": map[string]any{"gte": since.UTC().Format(time.RFC3339)},
	})
}

// mineFilter says the project is the user's: they lead it, or they are
// a member. That is the whole definition, and membership in Linear is
// where it is maintained — owl does not infer it.
//
// It once also asked for "a project I have an open issue in", to catch
// a project nobody had added the user to. That clause cannot be
// expressed: `issues: { some: { assignee: { isMe }, state: { type: {
// nin: … } } } }` does not conjoin per issue — Linear matches when
// *some* issue is the user's and *some* issue is open, which in a
// project of 920 is always true, and `and:` inside `some` is no
// different. Deriving it client-side worked but bought a filter that
// disagreed with the project's own membership. Joining the project is
// the fix, and it fixes it for everyone reading Linear, not just here.
const mineFilter = `or: [ { lead: { isMe: { eq: true } } }, { members: { isMe: { eq: true } } } ]`

const openProjectsQuery = `query($first: Int!, $after: String) {
  projects(first: $first, after: $after, orderBy: updatedAt, filter: {
    status: { type: { nin: ["completed", "canceled"] } }, ` + mineFilter + `
  }) { nodes { ` + projectFields + ` } pageInfo { hasNextPage endCursor } }
}`

const doneProjectsQuery = `query($first: Int!, $after: String, $since: DateTimeOrDuration!) {
  projects(first: $first, after: $after, orderBy: updatedAt, filter: {
    status: { type: { eq: "completed" } }, completedAt: { gte: $since }, ` + mineFilter + `
  }) { nodes { ` + projectFields + ` } pageInfo { hasNextPage endCursor } }
}`

// projectsWhere reads every page of a projects query.
func (l Linear) projectsWhere(query string, vars map[string]any) ([]Project, error) {
	var all []Project
	var after *string
	for {
		var r struct {
			Projects struct {
				Nodes    []Project `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"projects"`
		}
		page := map[string]any{"first": 50, "after": after}
		for k, v := range vars {
			page[k] = v
		}
		if err := l.query(query, page, &r); err != nil {
			return nil, err
		}
		all = append(all, r.Projects.Nodes...)
		if !r.Projects.PageInfo.HasNextPage || r.Projects.PageInfo.EndCursor == "" {
			return all, nil
		}
		after = &r.Projects.PageInfo.EndCursor
	}
}

// Projects: the open projects the user leads or belongs to, newest
// change first, every page of them. One query.
func (l Linear) Projects() ([]Project, error) {
	return l.projectsWhere(openProjectsQuery, nil)
}

// DoneProjects: the projects the user leads or belongs to that were
// completed since the given time.
func (l Linear) DoneProjects(since time.Time) ([]Project, error) {
	return l.projectsWhere(doneProjectsQuery, map[string]any{"since": since.UTC().Format(time.RFC3339)})
}

// Project looks one up by Linear's slug id (the tail of its URL) or
// its UUID — 90ms, against the seconds the filtered list can take, and
// the whole of what `owl project open <id>` needs.
func (l Linear) Project(id string) (Project, error) {
	var r struct {
		Project Project `json:"project"`
	}
	q := `query($id: String!) { project(id: $id) { ` + projectFields + ` } }`
	if err := l.query(q, map[string]any{"id": id}, &r); err != nil {
		return Project{}, err
	}
	return r.Project, nil
}

// projectIssuesQuery walks one project's open issues. Everyone's, not
// the user's: what a project is waiting on is rarely all on one desk,
// and the count that matters on a project row is yours over this.
const projectIssuesQuery = `query($id: String!, $first: Int!, $after: String) {
  project(id: $id) {
    issues(first: $first, after: $after, orderBy: updatedAt, filter: { state: { type: { nin: ["completed", "canceled"] } } }) {
      nodes { ` + issueFields + ` } pageInfo { hasNextPage endCursor }
    }
  }
}`

// ProjectIssues: the project's open issues, newest change first, every
// page.
func (l Linear) ProjectIssues(id string) ([]Issue, error) {
	var all []Issue
	var after *string
	for {
		var r struct {
			Project struct {
				Issues struct {
					Nodes    []Issue `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"issues"`
			} `json:"project"`
		}
		if err := l.query(projectIssuesQuery, map[string]any{"id": id, "first": 100, "after": after}, &r); err != nil {
			return nil, err
		}
		page := r.Project.Issues
		all = append(all, page.Nodes...)
		if !page.PageInfo.HasNextPage || page.PageInfo.EndCursor == "" {
			return all, nil
		}
		after = &page.PageInfo.EndCursor
	}
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
	return Linear{Token: secret{name: "linear", ref: cfg.Linear.Token, account: cfg.Linear.Account, notify: notify}, Team: cfg.Linear.Team}
}
