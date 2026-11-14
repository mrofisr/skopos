// Package evidence runs or records one alpha-exit gate as the JSON artifact
// consumed by repoctl exit-check.
package evidence

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
	"github.com/mrofisr/skopos/tools/repoctl/internal/exitcheck"
)

// Command implements `repoctl evidence`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "evidence" }

// Summary implements command.Command.
func (Command) Summary() string { return "run or record one alpha-exit gate" }

// Run implements command.Command.
func (Command) Run(ctx context.Context, env command.Env, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: repoctl evidence <run|record> --ec N --command CMD [--command args...]")
	}
	switch args[0] {
	case "run":
		return run(ctx, env, args[1:])
	case "record":
		return record(ctx, env, args[1:])
	default:
		return fmt.Errorf("unknown subcommand %q (want run or record)", args[0])
	}
}

type options struct {
	ec      int
	command string
	commit  string
	output  string
}

func addFlags(fs *flag.FlagSet, o *options) {
	fs.IntVar(&o.ec, "ec", 0, "exit criterion number, 1-7")
	fs.StringVar(&o.command, "command", "", "command line recorded in the evidence")
	fs.StringVar(&o.commit, "commit", "", "commit SHA; defaults to GITHUB_SHA or git HEAD")
	fs.StringVar(&o.output, "output", "", "output path; defaults to test/quality/evidence/ec-N.json")
}

func run(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("evidence run", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validate(o); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("evidence run: missing gate command after --")
	}
	if env.Run == nil {
		return fmt.Errorf("evidence run: no command runner")
	}

	gateErr := env.Run.Run(ctx, fs.Arg(0), fs.Args()[1:]...)
	status := "pass"
	if gateErr != nil {
		status = "fail"
	}
	path, err := write(ctx, env, o, status)
	if err != nil {
		if gateErr != nil {
			return fmt.Errorf("gate failed: %v; write evidence: %w", gateErr, err)
		}
		return err
	}
	fmt.Fprintf(env.Stdout, "evidence: EC%d %s -> %s\n", o.ec, status, path)
	if gateErr != nil {
		return fmt.Errorf("gate failed: %w", gateErr)
	}
	return nil
}

func record(ctx context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("evidence record", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	var o options
	addFlags(fs, &o)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := validate(o); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("evidence record: unexpected arguments after flags")
	}
	path, err := write(ctx, env, o, "pass")
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "evidence: EC%d pass -> %s\n", o.ec, path)
	return nil
}

func validate(o options) error {
	valid := false
	for _, criterion := range exitcheck.Criteria {
		if criterion.Number == o.ec {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("evidence: --ec must be one of 1-7")
	}
	if strings.TrimSpace(o.command) == "" {
		return fmt.Errorf("evidence: --command is required")
	}
	return nil
}

func write(ctx context.Context, env command.Env, o options, status string) (string, error) {
	commit := strings.TrimSpace(o.commit)
	if commit == "" {
		commit = strings.TrimSpace(env.Getenv("GITHUB_SHA"))
	}
	if commit == "" {
		if env.Run == nil {
			return "", fmt.Errorf("evidence: cannot resolve commit without GITHUB_SHA or a command runner")
		}
		out, err := env.Run.Output(ctx, "git", "rev-parse", "HEAD")
		if err != nil {
			return "", fmt.Errorf("evidence: resolve commit: %w", err)
		}
		commit = strings.TrimSpace(string(out))
	}
	if commit == "" {
		return "", fmt.Errorf("evidence: commit is empty")
	}
	now := env.Now()
	if now.IsZero() {
		return "", fmt.Errorf("evidence: timestamp is zero")
	}
	path := strings.TrimSpace(o.output)
	if path == "" {
		path = filepath.Join("test", "quality", "evidence", fmt.Sprintf("ec-%d.json", o.ec))
	}
	ev := exitcheck.Evidence{
		EC:        o.ec,
		Status:    status,
		Commit:    commit,
		Timestamp: now.UTC(),
		Command:   strings.TrimSpace(o.command),
	}
	data, err := json.MarshalIndent(ev, "", "  ")
	if err != nil {
		return "", fmt.Errorf("evidence: encode: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("evidence: create directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ec-*.tmp")
	if err != nil {
		return "", fmt.Errorf("evidence: create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("evidence: write: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return "", fmt.Errorf("evidence: chmod: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("evidence: close: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("evidence: rename: %w", err)
	}
	return path, nil
}
