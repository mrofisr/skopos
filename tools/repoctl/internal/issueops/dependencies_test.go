package issueops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

func TestClientListBlockedBy(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues/5/dependencies/blocked_by" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `[{"id":101,"number":1,"title":"AX-01: x","state":"closed","body":"b","labels":[{"name":"ec:6"}]}]`)
	}))
	blockers, err := client.ListBlockedBy(context.Background(), 5)
	if err != nil {
		t.Fatalf("ListBlockedBy: %v", err)
	}
	if len(blockers) != 1 || blockers[0].ID != 101 || blockers[0].Number != 1 || blockers[0].State != "closed" || blockers[0].Labels[0] != "ec:6" {
		t.Fatalf("blockers = %#v", blockers)
	}
}

func TestClientAddBlockedBy(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/repo/issues/5/dependencies/blocked_by" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			IssueID int64 `json:"issue_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode: %v", err)
		}
		if payload.IssueID != 101 {
			t.Errorf("issue_id = %d, want 101", payload.IssueID)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":101}`)
	}))
	if err := client.AddBlockedBy(context.Background(), 5, 101); err != nil {
		t.Fatalf("AddBlockedBy: %v", err)
	}
}

func TestClientRemoveBlockedBy(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/repos/owner/repo/issues/5/dependencies/blocked_by/101" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	if err := client.RemoveBlockedBy(context.Background(), 5, 101); err != nil {
		t.Fatalf("RemoveBlockedBy: %v", err)
	}
}

func TestClientBlockedByPropagatesErrors(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	ctx := context.Background()
	if _, err := client.ListBlockedBy(ctx, 5); err == nil {
		t.Error("ListBlockedBy: want an error")
	}
	if err := client.AddBlockedBy(ctx, 5, 101); err == nil {
		t.Error("AddBlockedBy: want an error")
	}
	if err := client.RemoveBlockedBy(ctx, 5, 101); err == nil {
		t.Error("RemoveBlockedBy: want an error")
	}
}
