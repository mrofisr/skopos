package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const cliProvisioning = "datasources:\n  - name: Skopos Prometheus\n    type: prometheus\n    uid: skopos-prometheus\n"

func TestRunHealthy(t *testing.T) {
	server := httptest.NewServer(fakeGrafana(t))
	defer server.Close()

	var stdout bytes.Buffer
	err := run([]string{
		"--grafana-url", server.URL,
		"--provisioning", writeProvisioning(t, cliProvisioning),
	}, &stdout, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "1 datasources healthy") {
		t.Fatalf("stdout = %q, want a healthy summary", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Skopos Prometheus (skopos-prometheus) OK") {
		t.Fatalf("stdout = %q, want the datasource name and uid", stdout.String())
	}
}

func TestRunUnhealthyDatasourceFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/health") {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"message":"connection refused"}`))
			return
		}
		_, _ = w.Write([]byte(`[{"uid":"skopos-prometheus","name":"Skopos Prometheus","type":"prometheus"}]`))
	}))
	defer server.Close()

	err := run([]string{
		"--grafana-url", server.URL,
		"--provisioning", writeProvisioning(t, cliProvisioning),
	}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded on an unhealthy datasource, want an error")
	}
	if !strings.Contains(err.Error(), "Skopos Prometheus") {
		t.Fatalf("error = %q, want the datasource name", err)
	}
}

func TestRunMissingProvisioningFails(t *testing.T) {
	err := run([]string{"--provisioning", filepath.Join(t.TempDir(), "absent.yaml")}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run succeeded without provisioning, want an error")
	}
	if !strings.Contains(err.Error(), "provisioning") {
		t.Fatalf("error = %q, want a provisioning error", err)
	}
}

func TestRunRejectsInvalidTimeout(t *testing.T) {
	err := run([]string{
		"--grafana-url", "http://127.0.0.1:1",
		"--provisioning", writeProvisioning(t, cliProvisioning),
		"--timeout", "soon",
	}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run accepted an invalid timeout, want an error")
	}
}

func TestRunUsesEnvironmentDefaults(t *testing.T) {
	server := httptest.NewServer(fakeGrafana(t))
	defer server.Close()
	t.Setenv("GRAFANA_URL", server.URL)
	t.Setenv("GRAFANA_PROVISIONING", writeProvisioning(t, cliProvisioning))

	var stdout bytes.Buffer
	if err := run(nil, &stdout, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(stdout.String(), "1 datasources healthy") {
		t.Fatalf("stdout = %q, want a healthy summary", stdout.String())
	}
}

func writeProvisioning(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "skopos.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write provisioning: %v", err)
	}
	return path
}

func fakeGrafana(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/datasources", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"uid":"skopos-prometheus","name":"Skopos Prometheus","type":"prometheus"}]`))
	})
	mux.HandleFunc("GET /api/datasources/uid/skopos-prometheus/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"OK","message":"Datasource is working"}`))
	})
	return mux
}
