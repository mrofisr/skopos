// Package releaseverify checks that a published Skopos release is signed,
// attested, complete, and internally consistent. It runs external tools through
// an injectable Runner so tests never touch the network.
package releaseverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const (
	releaseWorkflow = ".github/workflows/release.yml"
	oidcIssuer      = "https://token.actions.githubusercontent.com"
)

// Runner runs an external command and returns its standard output. Implementations
// should include a useful error when the command fails.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ExecRunner runs commands from PATH.
type ExecRunner struct{}

// Run executes name with args and returns stdout. The exit status and stderr
// are folded into the error so callers can fail closed with context.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	output, err := cmd.Output()
	if err == nil {
		return output, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if detail := strings.TrimSpace(string(exitErr.Stderr)); detail != "" {
			return output, fmt.Errorf("%s: %w: %s", name, err, detail)
		}
	}
	return output, fmt.Errorf("%s: %w", name, err)
}

// Config is one release to verify.
type Config struct {
	// Repo is the GitHub repository in owner/name form.
	Repo string
	// ImageRepository is the gateway image repository without a tag.
	ImageRepository string
	// ChartRef is the OCI chart repository without a version or oci:// prefix.
	ChartRef string
	// Tag is the release tag, with or without the leading v.
	Tag string
	// ValuesFile is the release values file attached to the GitHub release.
	ValuesFile string
	// Runner defaults to ExecRunner when nil.
	Runner Runner
}

var releaseTag = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

// Verify checks every release artifact and returns the first failure. Order:
// local values, image and chart signatures, provenance, SBOM assets, and
// digest equality.
func Verify(ctx context.Context, cfg Config) error {
	if strings.TrimSpace(cfg.Repo) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(cfg.ImageRepository) == "" {
		return errors.New("image repository is required")
	}
	if strings.TrimSpace(cfg.ChartRef) == "" {
		return errors.New("chart reference is required")
	}
	if strings.TrimSpace(cfg.ValuesFile) == "" {
		return errors.New("values file is required")
	}
	if !releaseTag.MatchString(strings.TrimSpace(cfg.Tag)) {
		return fmt.Errorf("invalid release tag %q: expected vX.Y.Z", cfg.Tag)
	}
	runner := cfg.Runner
	if runner == nil {
		runner = ExecRunner{}
	}

	tag := strings.TrimSpace(cfg.Tag)
	if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	version := strings.TrimPrefix(tag, "v")
	repo := strings.Trim(strings.TrimSpace(cfg.Repo), "/")
	imageRepository := strings.TrimRight(strings.TrimSpace(cfg.ImageRepository), "/")
	chartRef := strings.TrimRight(strings.TrimSpace(cfg.ChartRef), "/")
	chartRef = strings.TrimPrefix(chartRef, "oci://")

	valuesDigest, err := readValuesDigest(cfg.ValuesFile)
	if err != nil {
		return err
	}

	identity := fmt.Sprintf("https://github.com/%s/%s@refs/tags/%s", repo, releaseWorkflow, tag)
	imageRef := imageRepository + ":" + version
	chartImageRef := chartRef + ":" + version

	imageOutput, err := runner.Run(ctx, "cosign", "verify", imageRef,
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", oidcIssuer)
	if err != nil {
		return fmt.Errorf("verify image signature: %w", err)
	}
	imageDigest, err := cosignDigest(imageOutput)
	if err != nil {
		return fmt.Errorf("verify image signature: %w", err)
	}

	chartOutput, err := runner.Run(ctx, "cosign", "verify", chartImageRef,
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", oidcIssuer)
	if err != nil {
		return fmt.Errorf("verify chart signature: %w", err)
	}
	if _, err := cosignDigest(chartOutput); err != nil {
		return fmt.Errorf("verify chart signature: %w", err)
	}

	signer := repo + "/" + releaseWorkflow
	for _, subject := range []struct {
		name string
		ref  string
	}{
		{name: "image", ref: imageRef},
		{name: "chart", ref: chartImageRef},
	} {
		if _, err := runner.Run(ctx, "gh", "attestation", "verify", "oci://"+subject.ref,
			"--repo", repo,
			"--signer-workflow", signer); err != nil {
			return fmt.Errorf("verify %s provenance: %w", subject.name, err)
		}
	}

	assetOutput, err := runner.Run(ctx, "gh", "release", "view", tag,
		"--repo", repo,
		"--json", "assets",
		"--jq", ".assets[].name")
	if err != nil {
		return fmt.Errorf("list release assets: %w", err)
	}
	assets := make(map[string]bool)
	for _, name := range strings.Fields(string(assetOutput)) {
		assets[name] = true
	}
	for _, asset := range []string{
		"skopos-gateway-" + version + ".spdx.json",
		"skopos-chart-" + version + ".spdx.json",
	} {
		if !assets[asset] {
			return fmt.Errorf("release assets: missing %s", asset)
		}
	}

	if valuesDigest != imageDigest {
		return fmt.Errorf("values.release.yaml digest %s does not match image digest %s",
			valuesDigest, imageDigest)
	}
	return nil
}

func cosignDigest(output []byte) (string, error) {
	var results []struct {
		Critical struct {
			Image struct {
				DockerManifestDigest string `json:"docker-manifest-digest"`
			} `json:"image"`
		} `json:"critical"`
	}
	if err := json.Unmarshal(output, &results); err != nil {
		return "", fmt.Errorf("decode cosign output: %w", err)
	}
	for _, result := range results {
		digest := strings.TrimSpace(result.Critical.Image.DockerManifestDigest)
		if digest != "" {
			return digest, nil
		}
	}
	return "", errors.New("cosign output has no manifest digest")
}

func readValuesDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read values file: %w", err)
	}
	digest := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "digest:") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "digest:"))
		value = strings.Trim(value, `"'`)
		if value == "" {
			return "", errors.New("values.release.yaml has an empty digest")
		}
		if digest != "" {
			return "", errors.New("values.release.yaml has more than one digest")
		}
		digest = value
	}
	if digest == "" {
		return "", errors.New("values.release.yaml has no image digest")
	}
	return digest, nil
}
