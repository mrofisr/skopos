package grafanacheck

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/tools/repoctl/internal/command"
)

const provisioningFixture = `apiVersion: 1
datasources:
  - name: Prometheus
    uid: prom
    type: prometheus
    url: http://prometheus:9090
`

func testEnv() (command.Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return command.Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(0, 0) },
	}, stdout, stderr
}

func writeProvisioning(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "datasources.yaml")
	if err := os.WriteFile(path, []byte(provisioningFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func grafanaServer(t *testing.T, health string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/datasources":
			_, _ = io.WriteString(w, `[{"name":"Prometheus","uid":"prom","type":"prometheus"}]`)
		case "/api/datasources/uid/prom/health":
			_, _ = io.WriteString(w, health)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "grafana-check" {
		t.Fatalf("Name = %q, want grafana-check", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestGrafanaCheckHealthy(t *testing.T) {
	server := grafanaServer(t, `{"status":"OK","message":""}`)
	provisioning := writeProvisioning(t)
	env, stdout, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL, "--provisioning", provisioning,
	})
	if err != nil {
		t.Fatalf("grafana-check: %v", err)
	}
	for _, want := range []string{"Prometheus (prom) OK", "1 datasources healthy"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestGrafanaCheckUsesEnvDefaults(t *testing.T) {
	server := grafanaServer(t, `{"status":"OK"}`)
	provisioning := writeProvisioning(t)
	env, _, _ := testEnv()
	env.Getenv = func(key string) string {
		switch key {
		case "GRAFANA_URL":
			return server.URL
		case "GRAFANA_PROVISIONING":
			return provisioning
		default:
			return ""
		}
	}
	if err := (Command{}).Run(context.Background(), env, nil); err != nil {
		t.Fatalf("grafana-check: %v", err)
	}
}

func TestGrafanaCheckUnhealthy(t *testing.T) {
	server := grafanaServer(t, `{"status":"ERROR","message":"no route to host"}`)
	provisioning := writeProvisioning(t)
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL, "--provisioning", provisioning,
	})
	if err == nil || !strings.Contains(err.Error(), "unhealthy") {
		t.Fatalf("error = %v, want an unhealthy error", err)
	}
}

func TestGrafanaCheckRejectsNonPositiveTimeout(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{"--timeout", "0s"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("error = %v, want a timeout error", err)
	}
}

func TestGrafanaCheckMissingProvisioning(t *testing.T) {
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--provisioning", filepath.Join(t.TempDir(), "missing.yaml"),
	})
	if err == nil || !strings.Contains(err.Error(), "read provisioning") {
		t.Fatalf("error = %v, want a provisioning error", err)
	}
}

func TestGrafanaCheckRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("grafana-check accepted an unknown flag")
	}
}

func TestGrafanaCheckTruncatesLongErrorBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, strings.Repeat("upstream unavailable ", 20))
	}))
	t.Cleanup(server.Close)
	provisioning := writeProvisioning(t)
	env, _, _ := testEnv()
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL, "--provisioning", provisioning,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("error = %v, want the HTTP status", err)
	}
}
