package releaseverify

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/command"
)

// Command implements `repoctl release-verify`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "release-verify" }

// Summary implements command.Command.
func (Command) Summary() string {
	return "verify a published release: signatures, attestations, SBOMs, digest"
}

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("release-verify", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	repo := fs.String("repo", env.Getenv("GITHUB_REPOSITORY"), "repository in owner/name form")
	imageRepository := fs.String("image-repository",
		envDefault(env, "IMAGE_REPOSITORY", defaultImageRepository(env.Getenv("GITHUB_REPOSITORY"))),
		"gateway image repository without a tag")
	chartRef := fs.String("chart-ref",
		envDefault(env, "CHART_REF", defaultChartRef(env.Getenv("GITHUB_REPOSITORY"))),
		"chart OCI repository without a version")
	tag := fs.String("tag", envDefault(env, "TAG", env.Getenv("VERSION")),
		"release tag, with or without the leading v")
	valuesFile := fs.String("values", envDefault(env, "VALUES_FILE", "values.release.yaml"),
		"release values file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*tag) == "" && fs.NArg() > 0 {
		*tag = fs.Arg(0)
	}
	if strings.TrimSpace(*tag) == "" {
		return errors.New("release tag is required: pass --tag, TAG, or VERSION")
	}
	if env.Run == nil {
		return errors.New("release verification needs a command runner")
	}

	cfg := Config{
		Repo:            *repo,
		ImageRepository: *imageRepository,
		ChartRef:        *chartRef,
		Tag:             *tag,
		ValuesFile:      *valuesFile,
		Runner:          envRunner{runner: env.Run},
	}
	if err := Verify(ctx, cfg); err != nil {
		return err
	}
	normalized := strings.TrimSpace(*tag)
	if !strings.HasPrefix(normalized, "v") {
		normalized = "v" + normalized
	}
	fmt.Fprintf(env.Stdout, "release %s verified\n", normalized)
	return nil
}

// envRunner adapts the injected command runner to the Runner interface this
// package verifies with.
type envRunner struct {
	runner command.Runner
}

// Run implements Runner.
func (r envRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return r.runner.Output(ctx, name, args...)
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

func envDefault(env command.Env, name, fallback string) string {
	if value := env.Getenv(name); value != "" {
		return value
	}
	return fallback
}
