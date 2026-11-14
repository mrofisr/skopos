// Package faults exercises the Collector queue across a backend outage and a
// Collector restart. It needs Docker and is skipped unless
// SKOPOS_FAULTS_TEST=1.
package faults

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const (
	collectorURL = "http://127.0.0.1:14318"
	healthURL    = "http://127.0.0.1:13134/"
	sinkAddr     = "0.0.0.0:19090"
)

func compose(t *testing.T, args ...string) string {
	t.Helper()
	full := append([]string{"compose", "-p", "skopos-faults", "-f", "docker-compose.yml"}, args...)
	out, err := exec.Command("docker", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(full, " "), err, out)
	}
	return string(out)
}

func waitHTTP(t *testing.T, method, rawURL string, status int, body []byte) []byte {
	t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, rawURL, reader)
		if err != nil {
			t.Fatal(err)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err == nil {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == status {
				return data
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s %s did not return %d", method, rawURL, status)
	return nil
}

func waitCollector(t *testing.T) {
	t.Helper()
	waitHTTP(t, http.MethodGet, healthURL, http.StatusOK, nil)
}

func sendLog(t *testing.T, message string) {
	t.Helper()
	payload := map[string]any{
		"resourceLogs": []any{map[string]any{
			"resource": map[string]any{
				"attributes": []any{map[string]any{
					"key":   "service.name",
					"value": map[string]any{"stringValue": "fault-test"},
				}},
			},
			"scopeLogs": []any{map[string]any{
				"logRecords": []any{map[string]any{
					"timeUnixNano": strconv.FormatInt(time.Now().UnixNano(), 10),
					"severityText": "INFO",
					"body":         map[string]any{"stringValue": message},
				}},
			}},
		}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	waitHTTP(t, http.MethodPost, collectorURL+"/v1/logs", http.StatusOK, data)
}

type sink struct {
	server *http.Server
	count  atomic.Int64
}

func startSink(t *testing.T) *sink {
	t.Helper()
	s := &sink{}
	deadline := time.Now().Add(10 * time.Second)
	for {
		listener, err := net.Listen("tcp", sinkAddr)
		if err == nil {
			s.server = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				s.count.Add(1)
				w.WriteHeader(http.StatusOK)
			})}
			go func() { _ = s.server.Serve(listener) }()
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("start sink: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *sink) stop() {
	if s != nil && s.server != nil {
		_ = s.server.Close()
	}
}

func waitSink(t *testing.T, s *sink, want int64) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if s.count.Load() >= want {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("sink received %d requests, want at least %d", s.count.Load(), want)
}

func TestCollectorQueueSurvivesBackendAndCollectorRestart(t *testing.T) {
	if os.Getenv("SKOPOS_FAULTS_TEST") != "1" {
		t.Skip("set SKOPOS_FAULTS_TEST=1 to run the Docker fault-injection test")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose v2 is not available")
	}

	compose(t, "down", "-v", "--remove-orphans")
	t.Cleanup(func() {
		if os.Getenv("SKOPOS_FAULTS_KEEP") == "1" {
			t.Log("keeping fault-injection stack")
			return
		}
		compose(t, "down", "-v", "--remove-orphans")
	})

	first := startSink(t)
	defer first.stop()

	compose(t, "up", "-d")
	waitCollector(t)

	sendLog(t, "before-outage")
	waitSink(t, first, 1)

	// Backend is down. The Collector must accept the log into its file queue.
	first.stop()
	sendLog(t, "queued-before-collector-restart")

	// Restart the Collector while the backend is still down. The queue is on a
	// named volume, so both queued batches must survive.
	compose(t, "restart", "collector")
	waitCollector(t)
	sendLog(t, "queued-after-collector-restart")

	second := startSink(t)
	defer second.stop()
	waitSink(t, second, 2)
}
