package grafanacheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Dashboard is the subset of a Grafana dashboard the acceptance check needs.
type Dashboard struct {
	Title  string  `json:"title"`
	Panels []Panel `json:"panels"`
}

// Panel is one dashboard panel, possibly a row with nested panels.
type Panel struct {
	Title   string   `json:"title"`
	Panels  []Panel  `json:"panels"`
	Targets []Target `json:"targets"`
}

// Target is one panel query.
type Target struct {
	RefID      string `json:"refId"`
	Expr       string `json:"expr"`
	Datasource struct {
		Type string `json:"type"`
		UID  string `json:"uid"`
	} `json:"datasource"`
}

// DashboardTarget is a flattened panel target.
type DashboardTarget struct {
	Panel string
	Expr  string
	UID   string
}

// LoadDashboard reads a dashboard JSON file.
func LoadDashboard(path string) (*Dashboard, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dashboard: %w", err)
	}
	var dashboard Dashboard
	if err := json.Unmarshal(data, &dashboard); err != nil {
		return nil, fmt.Errorf("decode dashboard: %w", err)
	}
	return &dashboard, nil
}

// Targets flattens the panel tree and returns every query with an expression.
func (d *Dashboard) Targets() []DashboardTarget {
	out := make([]DashboardTarget, 0)
	var walk func(panels []Panel)
	walk = func(panels []Panel) {
		for _, panel := range panels {
			for _, target := range panel.Targets {
				if strings.TrimSpace(target.Expr) == "" {
					continue
				}
				out = append(out, DashboardTarget{
					Panel: panel.Title,
					Expr:  target.Expr,
					UID:   target.Datasource.UID,
				})
			}
			walk(panel.Panels)
		}
	}
	walk(d.Panels)
	return out
}

// CheckDashboard runs every panel target through Grafana and requires a
// non-empty result. The first failure names the panel and the expression.
func (j Journey) CheckDashboard(ctx context.Context, dashboard *Dashboard, cfg JourneyConfig) ([]JourneyStep, error) {
	cfg = cfg.withDefaults()
	targets := dashboard.Targets()
	if len(targets) == 0 {
		return nil, errors.New("dashboard has no panel targets")
	}
	steps := make([]JourneyStep, 0, len(targets))
	for _, target := range targets {
		uid := target.UID
		if uid == "" || strings.Contains(uid, "$") {
			uid = cfg.SkoposUID
		}
		if uid == "" {
			uid = "skopos-prometheus"
		}
		frames, err := j.query(ctx, uid, target.Expr, cfg, false)
		if err != nil {
			return steps, fmt.Errorf("panel %q: %s: %w", target.Panel, target.Expr, err)
		}
		series := nonEmptyFrames(frames)
		if series == 0 {
			return steps, fmt.Errorf("panel %q: %s returned no series", target.Panel, target.Expr)
		}
		steps = append(steps, JourneyStep{
			Name:   target.Panel,
			Detail: fmt.Sprintf("%s (%d series)", target.Expr, series),
		})
	}
	return steps, nil
}

func nonEmptyFrames(frames []frame) int {
	count := 0
	for _, f := range frames {
		if len(f.Data.Values) < 2 {
			continue
		}
		if len(f.Data.Values[1]) > 0 {
			count++
		}
	}
	return count
}
