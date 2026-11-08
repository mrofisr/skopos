// Command skopos-release-verify checks a published Skopos release. The
// release-verify Make target and scripts/verify-release.sh call it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jawaracloud/skopos/internal/releaseverify"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "verify-release: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithRunner(args, stdout, stderr, releaseverify.ExecRunner{})
}

func runWithRunner(args []string, stdout, stderr io.Writer, runner releaseverify.Runner) error {
	flags := flag.NewFlagSet("skopos-release-verify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	repo := flags.String("repo", os.Getenv("GITHUB_REPOSITORY"), "repository in owner/name form")
	imageRepository := flags.String("image-repository",
		envDefault("IMAGE_REPOSITORY", defaultImageRepository(os.Getenv("GITHUB_REPOSITORY"))),
		"gateway image repository without a tag")
	chartRef := flags.String("chart-ref",
		envDefault("CHART_REF", defaultChartRef(os.Getenv("GITHUB_REPOSITORY"))),
		"chart OCI repository without a version")
	tag := flags.String("tag", envDefault("TAG", os.Getenv("VERSION")),
		"release tag, with or without the leading v")
	valuesFile := flags.String("values", envDefault("VALUES_FILE", "values.release.yaml"),
		"release values file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tag) == "" && flags.NArg() > 0 {
		*tag = flags.Arg(0)
	}
	if strings.TrimSpace(*tag) == "" {
		return errors.New("release tag is required: pass --tag, TAG, or VERSION")
	}

	cfg := releaseverify.Config{
		Repo:            *repo,
		ImageRepository: *imageRepository,
		ChartRef:        *chartRef,
		Tag:             *tag,
		ValuesFile:      *valuesFile,
		Runner:          runner,
	}
	if err := releaseverify.Verify(context.Background(), cfg); err != nil {
		return err
	}
	normalized := strings.TrimSpace(*tag)
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}
	fmt.Fprintf(stdout, "release %s verified\n", normalized)
	return nil
}

func defaultImageRepository(repo string) string {
	owner, _, ok := strings.Cut(strings.TrimSpace(repo), "/")
	if !ok || owner == "" {
		return ""
	}
	return "ghcr.io/" + owner + "/skopos-gateway"
}

func defaultChartRef(repo string) string {
	owner, _, ok := strings.Cut(strings.TrimSpace(repo), "/")
	if !ok || owner == "" {
		return ""
	}
	return "ghcr.io/" + owner + "/charts/skopos"
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
