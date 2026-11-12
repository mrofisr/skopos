package grafanacheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const dashboardFixture = `{
  "title": "OBI RED",
  "panels": [
    {"title": "Rate", "targets": [{"refId": "A", "expr": "sum(rate(foo[5m]))", "datasource": {"type": "prometheus", "uid": "skopos-prometheus"}}]},
    {"title": "Row", "panels": [
      {"title": "Errors", "targets": [{"refId": "A", "expr": "sum(rate(foo_total{code=~\"5..\"}[5m]))"}]}
    ]}
  ]
}`

func writeDashboard(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dashboard.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func dashboardServer(t *testing.T, values int, errorText string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if errorText != "" {
			_, _ = fmt.Fprintf(w, `{"results":{"A":{"status":422,"error":%q,"frames":[]}}}`, errorText)
			return
		}
		frames := make([]frame, 0, values)
		for i := 0; i < values; i++ {
			frames = append(frames, rangeFrame(time.Unix(1793491200, 0), 1))
		}
		writeFrames(w, frames)
	}))
	t.Cleanup(server.Close)
	return server
}

func TestDashboardTargetsFlattensRows(t *testing.T) {
	path := writeDashboard(t, dashboardFixture)
	dashboard, err := LoadDashboard(path)
	if err != nil {
		t.Fatal(err)
	}
	targets := dashboard.Targets()
	if len(targets) != 2 {
		t.Fatalf("targets = %d, want 2: %+v", len(targets), targets)
	}
	if targets[1].Panel != "Errors" {
		t.Fatalf("nested panel = %q, want Errors", targets[1].Panel)
	}
	if targets[1].UID != "" {
		t.Fatalf("nested UID = %q, want empty", targets[1].UID)
	}
}

func TestCheckDashboardPasses(t *testing.T) {
	path := writeDashboard(t, dashboardFixture)
	dashboard, err := LoadDashboard(path)
	if err != nil {
		t.Fatal(err)
	}
	server := dashboardServer(t, 2, "")
	runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	steps, err := runner.CheckDashboard(context.Background(), dashboard, JourneyConfig{SkoposUID: "skopos-prometheus"})
	if err != nil {
		t.Fatalf("CheckDashboard: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(steps))
	}
}

func TestCheckDashboardEmptyPanelFails(t *testing.T) {
	path := writeDashboard(t, dashboardFixture)
	dashboard, err := LoadDashboard(path)
	if err != nil {
		t.Fatal(err)
	}
	server := dashboardServer(t, 0, "")
	runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	_, err = runner.CheckDashboard(context.Background(), dashboard, JourneyConfig{SkoposUID: "skopos-prometheus"})
	if err == nil || !strings.Contains(err.Error(), `panel "Rate"`) {
		t.Fatalf("error = %v, want a Rate panel failure", err)
	}
}

func TestCheckDashboardUnsupportedFunctionFails(t *testing.T) {
	path := writeDashboard(t, dashboardFixture)
	dashboard, err := LoadDashboard(path)
	if err != nil {
		t.Fatal(err)
	}
	server := dashboardServer(t, 0, "prom: unsupported function \"mad\"")
	runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	_, err = runner.CheckDashboard(context.Background(), dashboard, JourneyConfig{SkoposUID: "skopos-prometheus"})
	if err == nil || !strings.Contains(err.Error(), "unsupported function") {
		t.Fatalf("error = %v, want the unsupported function name", err)
	}
}

func TestDashboardWithoutTargetsFails(t *testing.T) {
	path := writeDashboard(t, `{"title":"empty","panels":[{"title":"Text"}]}`)
	dashboard, err := LoadDashboard(path)
	if err != nil {
		t.Fatal(err)
	}
	server := dashboardServer(t, 1, "")
	runner := Journey{Client: Client{BaseURL: server.URL, HTTP: server.Client()}}
	if _, err := runner.CheckDashboard(context.Background(), dashboard, JourneyConfig{}); err == nil {
		t.Fatal("CheckDashboard accepted a dashboard without targets")
	}
}

func TestLoadDashboardErrors(t *testing.T) {
	if _, err := LoadDashboard(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("LoadDashboard accepted a missing file")
	}
	path := writeDashboard(t, `{`)
	if _, err := LoadDashboard(path); err == nil {
		t.Fatal("LoadDashboard accepted invalid JSON")
	}
}

func TestDashboardTargetDatasourceUID(t *testing.T) {
	var d Dashboard
	d.Panels = []Panel{{Title: "P", Targets: []Target{{
		RefID: "A", Expr: "up",
	}}}}
	d.Panels[0].Targets[0].Datasource.UID = "${datasource}"
	targets := d.Targets()
	if len(targets) != 1 {
		t.Fatalf("targets = %+v", targets)
	}
	raw, _ := json.Marshal(targets[0])
	if !strings.Contains(string(raw), "${datasource}") {
		t.Fatalf("target JSON = %s", raw)
	}
}

func TestCommandDashboard(t *testing.T) {
	now := time.Date(2026, 11, 10, 7, 0, 0, 0, time.UTC)
	fake := journeyFake{t: t, now: now, serviceCount: 6}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	dashboard := writeDashboard(t, dashboardFixture)
	provisioning := writeProvisioning(t)
	env, stdout, _ := testEnv()
	env.Now = func() time.Time { return now }
	err := (Command{}).Run(context.Background(), env, []string{
		"--grafana-url", server.URL,
		"--provisioning", provisioning,
		"--dashboard", dashboard,
	})
	if err != nil {
		t.Fatalf("grafana-check --dashboard: %v", err)
	}
	for _, want := range []string{"dashboard Rate OK", "2 dashboard panels passed"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
}

func TestDashboardTargetsSkipEmptyExpr(t *testing.T) {
	var d Dashboard
	d.Panels = []Panel{{Title: "P", Targets: []Target{{RefID: "A", Expr: " "}, {RefID: "B", Expr: "up"}}}}
	if targets := d.Targets(); len(targets) != 1 || targets[0].Expr != "up" {
		t.Fatalf("targets = %+v", targets)
	}
}

func TestNonEmptyFrames(t *testing.T) {
	if got := nonEmptyFrames([]frame{{}}); got != 0 {
		t.Fatalf("empty frame count = %d", got)
	}
	var f frame
	f.Data.Values = [][]json.RawMessage{{json.RawMessage("1")}}
	if got := nonEmptyFrames([]frame{f}); got != 0 {
		t.Fatalf("one-array frame count = %d", got)
	}
}
