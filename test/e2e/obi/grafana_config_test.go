package obi_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestGrafanaComposeServiceIsPinnedAndUnprivileged(t *testing.T) {
	block := composeService(t, "grafana")
	for _, want := range []string{
		"image: grafana/grafana:",
		"@sha256:",
		".render/grafana/provisioning:/etc/grafana/provisioning:ro",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("grafana service is missing %q", want)
		}
	}
	if regexp.MustCompile(`(?m)^\s+privileged:\s*true\s*$`).MatchString(block) {
		t.Error("grafana service must not run privileged")
	}
	digest := regexp.MustCompile(`@sha256:[0-9a-f]{64}`)
	if !digest.MatchString(block) {
		t.Error("grafana image is not pinned by a sha256 digest")
	}
}

func TestSeedServiceRunsBeforeGrafana(t *testing.T) {
	block := composeService(t, "seed")
	if !strings.Contains(block, "./seed.sh:/seed/seed.sh:ro") {
		t.Error("seed service does not mount seed.sh")
	}
	if !strings.Contains(block, "service_completed_successfully") {
		t.Error("seed service does not wait for schema initialization")
	}
}

func composeService(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("docker-compose.yml")
	if err != nil {
		t.Fatalf("read compose file: %v", err)
	}
	lines := strings.Split(string(data), "\n")
	start := -1
	for index, line := range lines {
		if line == "  "+name+":" {
			start = index
			continue
		}
		if start >= 0 && strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") && strings.HasSuffix(line, ":") {
			return strings.Join(lines[start:index], "\n")
		}
	}
	if start < 0 {
		t.Fatalf("compose file has no %s service", name)
	}
	return strings.Join(lines[start:], "\n")
}
