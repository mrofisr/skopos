package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubRunner struct {
	run func(name string, args ...string) ([]byte, error)
}

func (s stubRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return s.run(name, args...)
}

const cliVersion = "0.1.0-alpha.2"

func TestRunWithRunnerVerifies(t *testing.T) {
	var stdout bytes.Buffer
	err := runWithRunner([]string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--tag", "v" + cliVersion,
		"--values", writeCLIValues(t, "sha256:abc"),
	}, &stdout, io.Discard, cliPassingRunner(t, "sha256:abc"))
	if err != nil {
		t.Fatalf("runWithRunner: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want a verification summary", stdout.String())
	}
}

func TestRunRequiresTag(t *testing.T) {
	t.Setenv("TAG", "")
	t.Setenv("VERSION", "")
	err := runWithRunner(nil, io.Discard, io.Discard, cliPassingRunner(t, "sha256:abc"))
	if err == nil {
		t.Fatal("runWithRunner succeeded without a tag, want an error")
	}
	if !strings.Contains(err.Error(), "tag") {
		t.Fatalf("error = %q, want a tag error", err)
	}
}

func TestDefaultRepositories(t *testing.T) {
	if got := defaultImageRepository("acme/skopos"); got != "ghcr.io/acme/skopos-gateway" {
		t.Fatalf("defaultImageRepository = %q", got)
	}
	if got := defaultChartRef("acme/skopos"); got != "ghcr.io/acme/charts/skopos" {
		t.Fatalf("defaultChartRef = %q", got)
	}
	if got := defaultImageRepository("no-slash"); got != "" {
		t.Fatalf("defaultImageRepository(no-slash) = %q, want empty", got)
	}
	if got := defaultChartRef(""); got != "" {
		t.Fatalf("defaultChartRef(empty) = %q, want empty", got)
	}
}

func TestRunAcceptsPositionalTag(t *testing.T) {
	t.Setenv("TAG", "")
	t.Setenv("VERSION", "")
	var stdout bytes.Buffer
	args := []string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--values", writeCLIValues(t, "sha256:abc"),
		cliVersion,
	}
	if err := runWithRunner(args, &stdout, io.Discard, cliPassingRunner(t, "sha256:abc")); err != nil {
		t.Fatalf("runWithRunner: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want the normalized v-prefixed tag", stdout.String())
	}
}

func TestRunUsesEnvironmentDefaults(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/skopos")
	t.Setenv("IMAGE_REPOSITORY", "ghcr.io/acme/skopos-gateway")
	t.Setenv("CHART_REF", "ghcr.io/acme/charts/skopos")
	t.Setenv("TAG", "v"+cliVersion)
	var stdout bytes.Buffer
	if err := runWithRunner([]string{"--values", writeCLIValues(t, "sha256:abc")},
		&stdout, io.Discard, cliPassingRunner(t, "sha256:abc")); err != nil {
		t.Fatalf("runWithRunner: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want a verification summary", stdout.String())
	}
}

func TestRunReportsVerificationFailure(t *testing.T) {
	runner := stubRunner{run: func(name string, args ...string) ([]byte, error) {
		if name == "cosign" && strings.Contains(strings.Join(args, " "), "skopos-gateway:") {
			return nil, errors.New("no matching signatures")
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}
	err := runWithRunner([]string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--tag", "v" + cliVersion,
		"--values", writeCLIValues(t, "sha256:abc"),
	}, io.Discard, io.Discard, runner)
	if err == nil {
		t.Fatal("runWithRunner succeeded on an unsigned image, want an error")
	}
	if !strings.Contains(err.Error(), "image signature") {
		t.Fatalf("error = %q, want the image signature step", err)
	}
}

func TestRunRejectsMissingValuesFile(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "acme/skopos")
	t.Setenv("IMAGE_REPOSITORY", "ghcr.io/acme/skopos-gateway")
	t.Setenv("CHART_REF", "ghcr.io/acme/charts/skopos")
	t.Setenv("TAG", "v"+cliVersion)
	err := runWithRunner([]string{"--values", filepath.Join(t.TempDir(), "absent.yaml")},
		io.Discard, io.Discard, cliPassingRunner(t, "sha256:abc"))
	if err == nil {
		t.Fatal("runWithRunner succeeded without a values file, want an error")
	}
	if !strings.Contains(err.Error(), "values") {
		t.Fatalf("error = %q, want a values error", err)
	}
}

func writeCLIValues(t *testing.T, digest string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.release.yaml")
	contents := "gateway:\n  enabled: true\n  image:\n    digest: \"" + digest + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	return path
}

func cliPassingRunner(t *testing.T, digest string) stubRunner {
	t.Helper()
	return stubRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"` + digest + `"}}}]`), nil
		case name == "cosign":
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:chart"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, nil
		case name == "gh" && len(args) > 1 && args[0] == "release" && args[1] == "view":
			return []byte("skopos-gateway-" + cliVersion + ".spdx.json\nskopos-chart-" + cliVersion + ".spdx.json\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
}
