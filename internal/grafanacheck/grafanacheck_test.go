package grafanacheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const provisioningFile = "../../deploy/examples/grafana/provisioning/datasources/skopos.yaml"

func TestParseProvisioningFile(t *testing.T) {
	data, err := os.ReadFile(provisioningFile)
	if err != nil {
		t.Fatalf("read provisioning: %v", err)
	}
	datasources, err := ParseProvisioning(data)
	if err != nil {
		t.Fatalf("ParseProvisioning: %v", err)
	}
	if len(datasources) != 3 {
		t.Fatalf("datasources = %#v, want 3", datasources)
	}
	want := map[string]string{
		"skopos-prometheus": "prometheus",
		"skopos-loki":       "loki",
		"skopos-tempo":      "tempo",
	}
	for _, datasource := range datasources {
		if want[datasource.UID] != datasource.Type {
			t.Errorf("datasource %q has type %q, want %q", datasource.UID, datasource.Type, want[datasource.UID])
		}
		if datasource.Name == "" {
			t.Errorf("datasource %q has no name", datasource.UID)
		}
	}
}

func TestParseProvisioningRejectsMissingUID(t *testing.T) {
	_, err := ParseProvisioning([]byte("datasources:\n  - name: Broken\n    type: prometheus\n"))
	if err == nil {
		t.Fatal("ParseProvisioning accepted a datasource without a uid")
	}
	if !strings.Contains(err.Error(), "uid") {
		t.Fatalf("error = %q, want a uid error", err)
	}
}

func TestParseProvisioningPathReadsDatasourcesDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "datasources"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	contents := "datasources:\n  - name: One\n    type: loki\n    uid: one\n"
	if err := os.WriteFile(filepath.Join(dir, "datasources", "one.yaml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	datasources, err := ParseProvisioningPath(dir)
	if err != nil {
		t.Fatalf("ParseProvisioningPath: %v", err)
	}
	if len(datasources) != 1 || datasources[0].UID != "one" {
		t.Fatalf("datasources = %#v, want one", datasources)
	}
}

func TestCheckHealthy(t *testing.T) {
	expected := mustProvisioning(t)
	server := httptest.NewServer(fakeGrafana(t, expected, nil))
	defer server.Close()

	client := Client{BaseURL: server.URL}
	checked, err := client.Check(context.Background(), expected)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(checked) != len(expected) {
		t.Fatalf("checked = %d, want %d", len(checked), len(expected))
	}
}

func TestCheckStoppedGatewayFailsClearly(t *testing.T) {
	expected := mustProvisioning(t)
	server := httptest.NewServer(fakeGrafana(t, expected, func(w http.ResponseWriter, uid string) bool {
		if uid != "skopos-prometheus" {
			return false
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"message":"Post \"http://gateway:18081/api/v1/query\": dial tcp: connect: connection refused"}`))
		return true
	}))
	defer server.Close()

	_, err := Client{BaseURL: server.URL}.Check(context.Background(), expected)
	if err == nil {
		t.Fatal("Check succeeded with a stopped gateway, want an error")
	}
	for _, want := range []string{"Skopos Prometheus", "connection refused"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestCheckWrongCredentialsFail(t *testing.T) {
	expected := mustProvisioning(t)
	server := httptest.NewServer(fakeGrafana(t, expected, func(w http.ResponseWriter, uid string) bool {
		if uid != "skopos-loki" {
			return false
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
		return true
	}))
	defer server.Close()

	_, err := Client{BaseURL: server.URL}.Check(context.Background(), expected)
	if err == nil {
		t.Fatal("Check succeeded with wrong credentials, want an error")
	}
	for _, want := range []string{"Skopos Loki", "401", "Unauthorized"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want %q", err, want)
		}
	}
}

func TestCheckMissingDatasourceFails(t *testing.T) {
	expected := mustProvisioning(t)
	server := httptest.NewServer(fakeGrafana(t, expected[:1], nil))
	defer server.Close()

	_, err := Client{BaseURL: server.URL}.Check(context.Background(), expected)
	if err == nil {
		t.Fatal("Check succeeded with a missing datasource, want an error")
	}
	if !strings.Contains(err.Error(), "skopos-loki") || !strings.Contains(err.Error(), "not provisioned") {
		t.Fatalf("error = %q, want a not-provisioned error", err)
	}
}

func TestCheckFailsClosedOnMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "datasource list", body: `{"not":"a list"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := Client{BaseURL: server.URL}.Check(context.Background(), mustProvisioning(t))
			if err == nil {
				t.Fatal("Check succeeded on malformed JSON, want an error")
			}
		})
	}

	expected := mustProvisioning(t)
	server := httptest.NewServer(fakeGrafana(t, expected, func(w http.ResponseWriter, uid string) bool {
		if uid != "skopos-tempo" {
			return false
		}
		_, _ = w.Write([]byte("not json"))
		return true
	}))
	defer server.Close()
	if _, err := (Client{BaseURL: server.URL}).Check(context.Background(), expected); err == nil {
		t.Fatal("Check succeeded on malformed health JSON, want an error")
	}
}

func TestCheckSendsGrafanaBasicAuth(t *testing.T) {
	expected := mustProvisioning(t)
	handler := fakeGrafana(t, expected, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "admin" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()

	client := Client{BaseURL: server.URL, Username: "admin", Password: "secret"}
	if _, err := client.Check(context.Background(), expected); err != nil {
		t.Fatalf("Check with basic auth: %v", err)
	}
}

func mustProvisioning(t *testing.T) []Datasource {
	t.Helper()
	data, err := os.ReadFile(provisioningFile)
	if err != nil {
		t.Fatalf("read provisioning: %v", err)
	}
	datasources, err := ParseProvisioning(data)
	if err != nil {
		t.Fatalf("ParseProvisioning: %v", err)
	}
	return datasources
}

// fakeGrafana serves the datasource list from expected and calls fail for a
// health check it wants to fail. A nil fail serves OK for every datasource.
func fakeGrafana(t *testing.T, expected []Datasource, fail func(w http.ResponseWriter, uid string) bool) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/datasources", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(datasourceListJSON(expected)))
	})
	for _, datasource := range expected {
		uid := datasource.UID
		mux.HandleFunc("GET /api/datasources/uid/"+uid+"/health", func(w http.ResponseWriter, _ *http.Request) {
			if fail != nil && fail(w, uid) {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"OK","message":"Datasource is working"}`))
		})
	}
	return mux
}

func datasourceListJSON(datasources []Datasource) string {
	entries := make([]string, 0, len(datasources))
	for _, datasource := range datasources {
		entries = append(entries, `{"uid":"`+datasource.UID+`","name":"`+datasource.Name+`","type":"`+datasource.Type+`"}`)
	}
	return `[` + strings.Join(entries, ",") + `]`
}
