package issueops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ghapi"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	api, err := ghapi.New(ghapi.Config{
		BaseURL: server.URL,
		Repo:    "owner/repo",
		Token:   "token",
		HTTP:    server.Client(),
	})
	if err != nil {
		t.Fatalf("ghapi.New: %v", err)
	}
	return NewClient(api)
}

func TestClientListLabelsFollowsPagination(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("page") == "" {
			w.Header().Set("Link", `<`+server.URL+`/repos/owner/repo/labels?page=2>; rel="next"`)
			_, _ = io.WriteString(w, `[{"name":"ec:1","color":"fbca04","description":"one"}]`)
			return
		}
		_, _ = io.WriteString(w, `[{"name":"ec:2","color":"0e8a16","description":"two"}]`)
	}))
	t.Cleanup(server.Close)
	api, err := ghapi.New(ghapi.Config{BaseURL: server.URL, Repo: "owner/repo", Token: "token", HTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	labels, err := NewClient(api).ListLabels(context.Background())
	if err != nil {
		t.Fatalf("ListLabels: %v", err)
	}
	if len(labels) != 2 || labels[0].Name != "ec:1" || labels[1].Name != "ec:2" {
		t.Fatalf("labels = %#v, want ec:1 and ec:2", labels)
	}
}

func TestClientListIssuesSkipsPullRequests(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("state"); got != "all" {
			t.Errorf("state = %q, want all", got)
		}
		_, _ = io.WriteString(w, `[
			{"number":7,"title":"AX-01: x","state":"open","body":"body","labels":[{"name":"ec:1"}]},
			{"number":8,"title":"a pull request","state":"open","body":"pr","pull_request":{"url":"https://api.github.com/repos/owner/repo/pulls/8"},"labels":[]}
		]`)
	}))
	issues, err := client.ListIssues(context.Background())
	if err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 7 || len(issues[0].Labels) != 1 {
		t.Fatalf("issues = %#v, want only issue 7", issues)
	}
}

func TestClientGetIssue(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues/57" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"number":57,"title":"AX-01: x","state":"open","body":"body","labels":[{"name":"phase:alpha-exit"},{"name":"ec:6"}]}`)
	}))
	issue, err := client.GetIssue(context.Background(), 57)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 57 || issue.State != "open" || len(issue.Labels) != 2 || issue.Labels[0] != "phase:alpha-exit" {
		t.Fatalf("issue = %#v", issue)
	}
}

func TestClientCreateIssueSendsLabels(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/repo/issues" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload.Title != "AX-01: x" || payload.Body != "body" || strings.Join(payload.Labels, ",") != "ec:1,type:micro" {
			t.Errorf("payload = %#v", payload)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"number":42}`)
	}))
	number, err := client.CreateIssue(context.Background(), Issue{ID: "AX-01", Title: "AX-01: x", Body: "body"}, []string{"ec:1", "type:micro"})
	if err != nil {
		t.Fatalf("CreateIssue: %v", err)
	}
	if number != 42 {
		t.Fatalf("number = %d, want 42", number)
	}
}

func TestClientCreateIssueRequiresNumber(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	}))
	if _, err := client.CreateIssue(context.Background(), Issue{ID: "AX-01"}, nil); err == nil || !strings.Contains(err.Error(), "no number") {
		t.Fatalf("error = %v, want a missing-number error", err)
	}
}

func TestClientUpsertLabelCreates(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/repo/labels" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload Label
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload.Color != "fbca04" {
			t.Errorf("color = %q, want the # stripped", payload.Color)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	if err := client.UpsertLabel(context.Background(), Label{Name: "ec:1", Color: "#fbca04", Description: "one"}); err != nil {
		t.Fatalf("UpsertLabel: %v", err)
	}
}

func TestClientUpsertLabelUpdatesWhenItExists(t *testing.T) {
	var methods []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"message":"Validation Failed"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	if err := client.UpsertLabel(context.Background(), Label{Name: "ec:1", Color: "fbca04"}); err != nil {
		t.Fatalf("UpsertLabel: %v", err)
	}
	want := []string{"POST /repos/owner/repo/labels", "PATCH /repos/owner/repo/labels/ec:1"}
	if strings.Join(methods, ",") != strings.Join(want, ",") {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
}

func TestClientUpdateIssue(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/owner/repo/issues/7" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if payload.Title != "AX-01: new" || payload.Body != "new body" {
			t.Errorf("payload = %#v", payload)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	if err := client.UpdateIssue(context.Background(), 7, Issue{Title: "AX-01: new", Body: "new body"}); err != nil {
		t.Fatalf("UpdateIssue: %v", err)
	}
}

func TestClientAddAndRemoveLabels(t *testing.T) {
	var calls []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			var payload struct {
				Labels []string `json:"labels"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if strings.Join(payload.Labels, ",") != "ec:1,type:micro" {
				t.Errorf("labels = %v", payload.Labels)
			}
		}
		_, _ = io.WriteString(w, `[]`)
	}))
	ctx := context.Background()
	if err := client.AddLabels(ctx, 7, []string{"ec:1", "type:micro"}); err != nil {
		t.Fatalf("AddLabels: %v", err)
	}
	if err := client.RemoveLabels(ctx, 7, []string{"ec:2"}); err != nil {
		t.Fatalf("RemoveLabels: %v", err)
	}
	want := []string{
		"POST /repos/owner/repo/issues/7/labels",
		"DELETE /repos/owner/repo/issues/7/labels/ec:2",
	}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestClientLabelNoOps(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
	}))
	ctx := context.Background()
	if err := client.AddLabels(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveLabels(ctx, 7, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientPropagatesStatusError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	_, err := client.ListIssues(context.Background())
	var status *ghapi.StatusError
	if !errors.As(err, &status) || !status.IsNotFound() {
		t.Fatalf("error = %v, want a typed 404", err)
	}
}
