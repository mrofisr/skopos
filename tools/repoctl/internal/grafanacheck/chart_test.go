package grafanacheck

import (
	"path/filepath"
	"testing"
)

// TestChartDashboards keeps the dashboards shipped by the Helm chart
// parseable and queryable. The OBI dashboard is also loaded by the Grafana
// acceptance harness.
func TestChartDashboards(t *testing.T) {
	paths, err := filepath.Glob("../../../../deploy/helm/skopos/dashboards/*.json")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"skopos-overview": false,
		"obi-red":         false,
	}
	for _, path := range paths {
		dashboard, err := LoadDashboard(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if dashboard.Title == "" {
			t.Errorf("%s: title is empty", path)
		}
		if _, ok := want[dashboard.UID]; !ok {
			t.Errorf("%s: unexpected dashboard uid %q", path, dashboard.UID)
			continue
		}
		want[dashboard.UID] = true
		targets := dashboard.Targets()
		if len(targets) == 0 {
			t.Errorf("%s: no panel targets", path)
			continue
		}
		for _, target := range targets {
			if target.UID == "" {
				t.Errorf("%s: panel %q has no datasource uid", path, target.Panel)
			}
		}
	}
	for uid, seen := range want {
		if !seen {
			t.Errorf("dashboard %q was not found in %s", uid, paths)
		}
	}
}
