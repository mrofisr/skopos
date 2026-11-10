package command

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/jawaracloud/skopos/cmd/repoctl/internal/ghapi"
)

// Command is one repoctl subcommand. Run receives the remaining arguments
// (without the subcommand name) and the injected environment.
type Command interface {
	Name() string
	Summary() string
	Run(ctx context.Context, env Env, args []string) error
}

// Runner runs external tools. Checks depend on this interface instead of
// os/exec so tests can record and fake commands.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Env is the process environment a command may use. Every field is optional;
// Main fills safe defaults before calling a command.
type Env struct {
	Stdout    io.Writer
	Stderr    io.Writer
	Getenv    func(string) string
	Now       func() time.Time
	Run       Runner
	NewGitHub func(repo string) (*ghapi.Client, error)
}

// OSEnv returns an Env backed by the real process.
func OSEnv() Env {
	env := Env{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.Getenv,
		Now:    time.Now,
		Run:    ExecRunner{Stdout: os.Stdout, Stderr: os.Stderr},
	}
	env.NewGitHub = func(repo string) (*ghapi.Client, error) {
		return GitHubFromEnv(env, repo)
	}
	return env
}

// GitHubFromEnv builds a GitHub client from GITHUB_TOKEN, GH_TOKEN,
// GITHUB_REPOSITORY, and GITHUB_API_URL. The token is required.
func GitHubFromEnv(env Env, repo string) (*ghapi.Client, error) {
	if repo == "" {
		repo = env.Getenv("GITHUB_REPOSITORY")
	}
	token := env.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = env.Getenv("GH_TOKEN")
	}
	return ghapi.New(ghapi.Config{
		BaseURL: env.Getenv("GITHUB_API_URL"),
		Repo:    repo,
		Token:   token,
	})
}

func (e Env) normalized() Env {
	if e.Stdout == nil {
		e.Stdout = io.Discard
	}
	if e.Stderr == nil {
		e.Stderr = io.Discard
	}
	if e.Getenv == nil {
		e.Getenv = func(string) string { return "" }
	}
	if e.Now == nil {
		e.Now = time.Now
	}
	return e
}

// Registry is the set of commands repoctl accepts. Names are unique.
type Registry struct {
	commands map[string]Command
}

// NewRegistry validates commands and returns a registry. A nil command, an
// empty name, or a duplicate name is an error.
func NewRegistry(commands ...Command) (*Registry, error) {
	registry := &Registry{commands: make(map[string]Command, len(commands))}
	for _, cmd := range commands {
		if cmd == nil {
			return nil, errors.New("command: nil command")
		}
		name := strings.TrimSpace(cmd.Name())
		if name == "" {
			return nil, errors.New("command: empty name")
		}
		if _, exists := registry.commands[name]; exists {
			return nil, fmt.Errorf("command: duplicate name %q", name)
		}
		registry.commands[name] = cmd
	}
	return registry, nil
}

// Names returns the registered names in sorted order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Lookup returns one command by name.
func (r *Registry) Lookup(name string) (Command, bool) {
	cmd, ok := r.commands[name]
	return cmd, ok
}

// Usage writes the command list.
func (r *Registry) Usage(w io.Writer) {
	fmt.Fprintln(w, "usage: repoctl <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	for _, name := range r.Names() {
		fmt.Fprintf(w, "  %-20s %s\n", name, r.commands[name].Summary())
	}
}

// Main runs one command and returns the process exit code:
// 0 success or help, 1 command error, 2 usage error.
func Main(ctx context.Context, env Env, args []string, registry *Registry) int {
	env = env.normalized()
	if registry == nil {
		fmt.Fprintln(env.Stderr, "repoctl: no command registry")
		return 2
	}
	if len(args) == 0 {
		registry.Usage(env.Stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		registry.Usage(env.Stdout)
		return 0
	}
	cmd, ok := registry.Lookup(args[0])
	if !ok {
		fmt.Fprintf(env.Stderr, "repoctl: unknown command %q\n", args[0])
		registry.Usage(env.Stderr)
		return 2
	}
	err := cmd.Run(ctx, env, args[1:])
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(env.Stderr, "repoctl:", err)
	return 1
}

// ExecRunner is the production Runner.
type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Run executes a command and streams its output.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	return cmd.Run()
}

// Output executes a command and returns stdout, with stderr in the error.
func (r ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}
