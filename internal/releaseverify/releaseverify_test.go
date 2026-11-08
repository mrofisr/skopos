package releaseverify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	run func(name string, args ...string) ([]byte, error)
}

func (f fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	return f.run(name, args...)
}

const testVersion = "0.1.0-alpha.2"

func TestVerifyPublishedRelease(t *testing.T) {
	var calls []string
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, name+" "+joined)
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:abc"}}}]`), nil
		case name == "cosign" && strings.Contains(joined, "charts/skopos:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:def"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, nil
		case name == "gh" && len(args) > 1 && args[0] == "release" && args[1] == "view":
			return []byte("skopos-gateway-" + testVersion + ".spdx.json\nskopos-chart-" + testVersion + ".spdx.json\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
	cfg := Config{
		Repo:            "acme/skopos",
		ImageRepository: "ghcr.io/acme/skopos-gateway",
		ChartRef:        "ghcr.io/acme/charts/skopos",
		Tag:             "v" + testVersion,
		ValuesFile:      writeValues(t, "sha256:abc"),
		Runner:          runner,
	}
	if err := Verify(context.Background(), cfg); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	identity := "https://github.com/acme/skopos/.github/workflows/release.yml@refs/tags/v" + testVersion
	issuer := "https://token.actions.githubusercontent.com"
	for _, want := range []string{
		"cosign verify ghcr.io/acme/skopos-gateway:" + testVersion,
		"--certificate-identity " + identity,
		"--certificate-oidc-issuer " + issuer,
		"cosign verify ghcr.io/acme/charts/skopos:" + testVersion,
		"gh attestation verify oci://ghcr.io/acme/skopos-gateway:" + testVersion,
		"--signer-workflow acme/skopos/.github/workflows/release.yml",
		"gh release view v" + testVersion,
		"--json assets",
	} {
		found := false
		for _, call := range calls {
			if strings.Contains(call, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing call containing %q; calls: %v", want, calls)
		}
	}
}

func TestVerifyFailsOnUnsignedImage(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if name == "cosign" && strings.Contains(strings.Join(args, " "), "skopos-gateway:") {
			return nil, errors.New("no matching signatures")
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded on an unsigned image, want an error")
	} else if !strings.Contains(err.Error(), "image signature") {
		t.Fatalf("error = %q, want the image signature step", err)
	}
}

func TestVerifyFailsOnDigestMismatch(t *testing.T) {
	runner := passingRunner(t, "sha256:abc")
	cfg := baseConfig(t, "sha256:def", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded on a digest mismatch, want an error")
	} else if !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %q, want a digest mismatch", err)
	}
}

func TestVerifyFailsOnMissingSBOM(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:abc"}}}]`), nil
		case name == "cosign":
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:def"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, nil
		case name == "gh" && len(args) > 1 && args[0] == "release" && args[1] == "view":
			return []byte("skopos-chart-" + testVersion + ".spdx.json\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded without the image SBOM, want an error")
	} else if !strings.Contains(err.Error(), "skopos-gateway-"+testVersion+".spdx.json") {
		t.Fatalf("error = %q, want the missing asset name", err)
	}
}

func TestVerifyFailsOnMissingProvenance(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:abc"}}}]`), nil
		case name == "cosign":
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:def"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, errors.New("no attestations found")
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded without provenance, want an error")
	} else if !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("error = %q, want a provenance error", err)
	}
}

func TestVerifyRejectsInvalidTag(t *testing.T) {
	cfg := baseConfig(t, "sha256:abc", passingRunner(t, "sha256:abc"))
	cfg.Tag = "v0.1.0/../../latest"
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify accepted a path-like tag, want an error")
	}
}

func TestVerifyFailsOnChartSignature(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:abc"}}}]`), nil
		case name == "cosign":
			return nil, errors.New("no matching signatures")
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded on an unsigned chart, want an error")
	} else if !strings.Contains(err.Error(), "chart signature") {
		t.Fatalf("error = %q, want the chart signature step", err)
	}
}

func TestVerifyFailsOnMalformedCosignOutput(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		if name == "cosign" {
			return []byte("not json"), nil
		}
		return nil, fmt.Errorf("unexpected command: %s %v", name, args)
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded on malformed cosign output, want an error")
	} else if !strings.Contains(err.Error(), "image signature") {
		t.Fatalf("error = %q, want the image signature step", err)
	}
}

func TestVerifyFailsOnMissingChartSBOM(t *testing.T) {
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:abc"}}}]`), nil
		case name == "cosign":
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:def"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, nil
		case name == "gh" && len(args) > 1 && args[0] == "release" && args[1] == "view":
			return []byte("skopos-gateway-" + testVersion + ".spdx.json\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	if err := Verify(context.Background(), cfg); err == nil {
		t.Fatal("Verify succeeded without the chart SBOM, want an error")
	} else if !strings.Contains(err.Error(), "skopos-chart-"+testVersion+".spdx.json") {
		t.Fatalf("error = %q, want the missing asset name", err)
	}
}

func TestVerifyAcceptsATagWithoutV(t *testing.T) {
	var calls []string
	runner := fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, name+" "+joined)
		return passingRunner(t, "sha256:abc").Run(context.Background(), name, args...)
	}}
	cfg := baseConfig(t, "sha256:abc", runner)
	cfg.Tag = testVersion
	if err := Verify(context.Background(), cfg); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	identity := "https://github.com/acme/skopos/.github/workflows/release.yml@refs/tags/v" + testVersion
	found := false
	for _, call := range calls {
		if strings.Contains(call, "cosign verify ghcr.io/acme/skopos-gateway:"+testVersion) &&
			strings.Contains(call, identity) {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls do not normalize the tag to v%s: %v", testVersion, calls)
	}
}

func TestReadValuesDigestErrors(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     string
	}{
		{name: "missing", contents: "gateway:\n  enabled: true\n", want: "no image digest"},
		{name: "empty", contents: "digest: \"\"\n", want: "empty digest"},
		{name: "duplicate", contents: "digest: sha256:abc\ndigest: sha256:def\n", want: "more than one digest"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "values.release.yaml")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatalf("write values: %v", err)
			}
			if _, err := readValuesDigest(path); err == nil {
				t.Fatalf("readValuesDigest(%q) = nil, want %q", test.contents, test.want)
			} else if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func baseConfig(t *testing.T, valuesDigest string, runner Runner) Config {
	t.Helper()
	return Config{
		Repo:            "acme/skopos",
		ImageRepository: "ghcr.io/acme/skopos-gateway",
		ChartRef:        "ghcr.io/acme/charts/skopos",
		Tag:             "v" + testVersion,
		ValuesFile:      writeValues(t, valuesDigest),
		Runner:          runner,
	}
}

func passingRunner(t *testing.T, digest string) Runner {
	t.Helper()
	return fakeRunner{run: func(name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case name == "cosign" && strings.Contains(joined, "skopos-gateway:"):
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"` + digest + `"}}}]`), nil
		case name == "cosign":
			return []byte(`[{"critical":{"image":{"docker-manifest-digest":"sha256:chart"}}}]`), nil
		case name == "gh" && len(args) > 0 && args[0] == "attestation":
			return nil, nil
		case name == "gh" && len(args) > 1 && args[0] == "release" && args[1] == "view":
			return []byte("skopos-gateway-" + testVersion + ".spdx.json\nskopos-chart-" + testVersion + ".spdx.json\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %s", name, joined)
		}
	}}
}

func writeValues(t *testing.T, digest string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.release.yaml")
	contents := "gateway:\n  enabled: true\n  image:\n    digest: \"" + digest + "\"\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write values: %v", err)
	}
	return path
}
