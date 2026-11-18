// Package helm verifies that the chart maps admission cost values into the
// gateway and query deployments and rejects invalid combinations.
package helm

import (
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const chart = "../../deploy/helm/skopos"

func render(t *testing.T, sets ...string) (string, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	args := []string{"template", "skopos", chart, "--namespace", "observability", "--set", "gateway.enabled=true"}
	for _, set := range sets {
		args = append(args, "--set", set)
	}
	cmd := exec.Command(helm, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func assertEnv(t *testing.T, rendered, name, value string, want int) {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)- name: ` + regexp.QuoteMeta(name) + `\n\s+value: "` + regexp.QuoteMeta(value) + `"`)
	if got := len(pattern.FindAllString(rendered, -1)); got != want {
		t.Errorf("%s=%s occurrences = %d, want %d\n%s", name, value, got, want, rendered)
	}
}

func TestCostValuesMapToEnv(t *testing.T) {
	rendered, err := render(t)
	if err != nil {
		t.Fatalf("render defaults: %v\n%s", err, rendered)
	}
	assertEnv(t, rendered, "SKOPOS_ROWS_PER_SLOT", "200000", 1)
	assertEnv(t, rendered, "SKOPOS_INSTANT_FALLBACK_WEIGHT", "8", 1)
	assertEnv(t, rendered, "SKOPOS_WORK_WEIGHTING", "true", 1)

	rendered, err = render(t,
		"gateway.config.rowsPerSlot=2000",
		"gateway.config.instantFallbackWeight=7",
		"gateway.config.workWeighting=false",
	)
	if err != nil {
		t.Fatalf("render overrides: %v\n%s", err, rendered)
	}
	assertEnv(t, rendered, "SKOPOS_ROWS_PER_SLOT", "2000", 1)
	assertEnv(t, rendered, "SKOPOS_INSTANT_FALLBACK_WEIGHT", "7", 1)
	assertEnv(t, rendered, "SKOPOS_WORK_WEIGHTING", "false", 1)
}

func TestInvalidCostValuesRejected(t *testing.T) {
	cases := []struct {
		set  string
		want string
	}{
		{"gateway.config.rowsPerSlot=-1", "rowsPerSlot"},
		{"gateway.config.instantFallbackWeight=0", "instantFallbackWeight"},
		{"gateway.config.workWeighting=maybe", "workWeighting"},
	}
	for _, tc := range cases {
		t.Run(tc.set, func(t *testing.T) {
			out, err := render(t, tc.set)
			if err == nil {
				t.Fatalf("invalid value was accepted:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("error %q does not name %s", out, tc.want)
			}
		})
	}
}
