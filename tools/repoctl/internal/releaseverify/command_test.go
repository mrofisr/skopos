package releaseverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

const cliVersion = "0.1.0-alpha.2"

type commandRunner struct {
	output func(name string, args ...string) ([]byte, error)
}

func (f *commandRunner) Run(context.Context, string, ...string) error { return nil }

func (f *commandRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	return f.output(name, args...)
}

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

func writeCommandValues(t *testing.T, digest string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.release.yaml")
	contents := "gateway:\n  enabled: true\n  image:\n    digest: \"" + digest + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	return path
}

func commandPassingRunner(t *testing.T, digest string) *commandRunner {
	t.Helper()
	return &commandRunner{output: func(name string, args ...string) ([]byte, error) {
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

func TestCommandIdentity(t *testing.T) {
	cmd := Command{}
	if cmd.Name() != "release-verify" {
		t.Fatalf("Name = %q, want release-verify", cmd.Name())
	}
	if cmd.Summary() == "" {
		t.Fatal("Summary is empty")
	}
}

func TestReleaseVerifyPasses(t *testing.T) {
	env, stdout, _ := testEnv()
	env.Run = commandPassingRunner(t, "sha256:abc")
	err := (Command{}).Run(context.Background(), env, []string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--tag", "v" + cliVersion,
		"--values", writeCommandValues(t, "sha256:abc"),
	})
	if err != nil {
		t.Fatalf("release-verify: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want a verification summary", stdout.String())
	}
}

func TestReleaseVerifyRequiresTag(t *testing.T) {
	env, _, _ := testEnv()
	env.Run = commandPassingRunner(t, "sha256:abc")
	err := (Command{}).Run(context.Background(), env, nil)
	if err == nil || !strings.Contains(err.Error(), "tag") {
		t.Fatalf("error = %v, want a tag error", err)
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

func TestReleaseVerifyAcceptsPositionalTag(t *testing.T) {
	env, stdout, _ := testEnv()
	env.Run = commandPassingRunner(t, "sha256:abc")
	err := (Command{}).Run(context.Background(), env, []string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--values", writeCommandValues(t, "sha256:abc"),
		cliVersion,
	})
	if err != nil {
		t.Fatalf("release-verify: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want the normalized v-prefixed tag", stdout.String())
	}
}

func TestReleaseVerifyUsesEnvironmentDefaults(t *testing.T) {
	env, stdout, _ := testEnv()
	env.Getenv = func(key string) string {
		switch key {
		case "GITHUB_REPOSITORY":
			return "acme/skopos"
		case "IMAGE_REPOSITORY":
			return "ghcr.io/acme/skopos-gateway"
		case "CHART_REF":
			return "ghcr.io/acme/charts/skopos"
		case "TAG":
			return "v" + cliVersion
		default:
			return ""
		}
	}
	env.Run = commandPassingRunner(t, "sha256:abc")
	if err := (Command{}).Run(context.Background(), env, []string{"--values", writeCommandValues(t, "sha256:abc")}); err != nil {
		t.Fatalf("release-verify: %v", err)
	}
	if !strings.Contains(stdout.String(), "release v"+cliVersion+" verified") {
		t.Fatalf("stdout = %q, want a verification summary", stdout.String())
	}
}

func TestReleaseVerifyReportsFailure(t *testing.T) {
	env, _, _ := testEnv()
	env.Run = &commandRunner{output: func(name string, args ...string) ([]byte, error) {
		if name == "cosign" && strings.Contains(strings.Join(args, " "), "skopos-gateway:") {
			return nil, errors.New("no matching signatures")
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}
	err := (Command{}).Run(context.Background(), env, []string{
		"--repo", "acme/skopos",
		"--image-repository", "ghcr.io/acme/skopos-gateway",
		"--chart-ref", "ghcr.io/acme/charts/skopos",
		"--tag", "v" + cliVersion,
		"--values", writeCommandValues(t, "sha256:abc"),
	})
	if err == nil || !strings.Contains(err.Error(), "image signature") {
		t.Fatalf("error = %v, want the image signature step", err)
	}
}

func TestReleaseVerifyRejectsMissingValuesFile(t *testing.T) {
	env, _, _ := testEnv()
	env.Getenv = func(key string) string {
		switch key {
		case "GITHUB_REPOSITORY":
			return "acme/skopos"
		case "IMAGE_REPOSITORY":
			return "ghcr.io/acme/skopos-gateway"
		case "CHART_REF":
			return "ghcr.io/acme/charts/skopos"
		case "TAG":
			return "v" + cliVersion
		default:
			return ""
		}
	}
	env.Run = commandPassingRunner(t, "sha256:abc")
	err := (Command{}).Run(context.Background(), env, []string{
		"--values", filepath.Join(t.TempDir(), "absent.yaml"),
	})
	if err == nil || !strings.Contains(err.Error(), "values") {
		t.Fatalf("error = %v, want a values error", err)
	}
}

func TestReleaseVerifyRequiresARunner(t *testing.T) {
	env, _, _ := testEnv()
	env.Getenv = func(key string) string {
		if key == "TAG" {
			return "v" + cliVersion
		}
		return ""
	}
	err := (Command{}).Run(context.Background(), env, nil)
	if err == nil || !strings.Contains(err.Error(), "command runner") {
		t.Fatalf("error = %v, want a runner error", err)
	}
}

func TestReleaseVerifyRejectsUnknownFlag(t *testing.T) {
	env, _, _ := testEnv()
	env.Run = commandPassingRunner(t, "sha256:abc")
	if err := (Command{}).Run(context.Background(), env, []string{"--nope"}); err == nil {
		t.Fatal("release-verify accepted an unknown flag")
	}
}
