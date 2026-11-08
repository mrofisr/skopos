package codescanning

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name    string
		alerts  []Alert
		wantErr bool
		want    string
	}{
		{
			name: "no alerts",
		},
		{
			name: "low and medium only",
			alerts: []Alert{
				{Number: 1, State: "open", Rule: Rule{ID: "js/low", SecuritySeverityLevel: "low"}},
				{Number: 2, State: "open", Rule: Rule{ID: "js/medium", SecuritySeverityLevel: "medium"}},
			},
		},
		{
			name: "one high",
			alerts: []Alert{
				{Number: 7, State: "open", Rule: Rule{ID: "js/prototype-pollution", SecuritySeverityLevel: "high"}},
			},
			wantErr: true,
			want:    "#7 js/prototype-pollution (high)",
		},
		{
			name: "one critical",
			alerts: []Alert{
				{Number: 9, State: "open", Rule: Rule{ID: "js/code-injection", SecuritySeverityLevel: "critical"}},
			},
			wantErr: true,
			want:    "#9 js/code-injection (critical)",
		},
		{
			name: "closed high alert is ignored",
			alerts: []Alert{
				{Number: 4, State: "closed", Rule: Rule{ID: "js/old", SecuritySeverityLevel: "high"}},
			},
		},
		{
			name: "missing severity is ignored",
			alerts: []Alert{
				{Number: 5, State: "open", Rule: Rule{ID: "js/unknown"}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Evaluate(test.alerts)
			if test.wantErr {
				if err == nil {
					t.Fatalf("Evaluate(%#v) = nil, want an error", test.alerts)
				}
				if !strings.Contains(err.Error(), test.want) {
					t.Fatalf("error = %q, want %q", err, test.want)
				}
				return
			}
			if err != nil {
				t.Fatalf("Evaluate(%#v): %v", test.alerts, err)
			}
		})
	}
}

func TestOpenAlertsPaginatesAndSendsAuth(t *testing.T) {
	var requests []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Clone(context.Background()))
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q, want Bearer test-token", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state = %q, want open", got)
		}
		if got := r.URL.Query().Get("per_page"); got != "100" {
			t.Errorf("per_page = %q, want 100", got)
		}
		switch r.URL.Query().Get("page") {
		case "", "1":
			w.Header().Set("Link", `<`+serverURL(r)+`/repos/acme/skopos/code-scanning/alerts?state=open&per_page=100&page=2>; rel="next"`)
			writeAlerts(t, w, []Alert{
				{Number: 1, State: "open", Rule: Rule{ID: "js/a", SecuritySeverityLevel: "low"}},
				{Number: 2, State: "open", Rule: Rule{ID: "js/b", SecuritySeverityLevel: "medium"}},
			})
		case "2":
			writeAlerts(t, w, []Alert{
				{Number: 3, State: "open", Rule: Rule{ID: "js/c", SecuritySeverityLevel: "low"}},
			})
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
			http.Error(w, "unexpected page", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Repo: "acme/skopos", Token: "test-token"}
	alerts, err := client.OpenAlerts(context.Background())
	if err != nil {
		t.Fatalf("OpenAlerts: %v", err)
	}
	if len(alerts) != 3 {
		t.Fatalf("alerts = %#v, want 3", alerts)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
}

func TestOpenAlertsFailsClosedOnAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Repo: "acme/skopos", Token: "test-token"}
	if _, err := client.OpenAlerts(context.Background()); err == nil {
		t.Fatal("OpenAlerts succeeded on a 502, want an error")
	} else if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %q, want the status code", err)
	}
}

func TestOpenAlertsFailsClosedOnMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"not":"an array"}`))
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Repo: "acme/skopos", Token: "test-token"}
	if _, err := client.OpenAlerts(context.Background()); err == nil {
		t.Fatal("OpenAlerts succeeded on malformed JSON, want an error")
	}
}

func writeAlerts(t *testing.T, w http.ResponseWriter, alerts []Alert) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(alerts); err != nil {
		t.Fatalf("encode alerts: %v", err)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
