package command

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"strings"
	"testing"
	"time"
)

type fakeCommand struct {
	name    string
	summary string
	run     func(context.Context, Env, []string) error
}

func (f fakeCommand) Name() string    { return f.name }
func (f fakeCommand) Summary() string { return f.summary }
func (f fakeCommand) Run(ctx context.Context, env Env, args []string) error {
	if f.run == nil {
		return nil
	}
	return f.run(ctx, env, args)
}

func testEnv() (Env, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	return Env{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Unix(0, 0) },
	}, stdout, stderr
}

func mustRegistry(t *testing.T, commands ...Command) *Registry {
	t.Helper()
	registry, err := NewRegistry(commands...)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return registry
}

func TestMainUnknownCommand(t *testing.T) {
	env, stdout, stderr := testEnv()
	registry := mustRegistry(t, fakeCommand{name: "known", summary: "known command"})
	code := Main(context.Background(), env, []string{"nope"}, registry)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Fatalf("stderr = %q, want unknown command", stderr.String())
	}
	if !strings.Contains(stderr.String(), "usage: repoctl") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestMainHelp(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		t.Run(arg, func(t *testing.T) {
			env, stdout, stderr := testEnv()
			called := false
			registry := mustRegistry(t, fakeCommand{
				name:    "alpha",
				summary: "first command",
				run: func(context.Context, Env, []string) error {
					called = true
					return nil
				},
			}, fakeCommand{name: "beta", summary: "second command"})
			code := Main(context.Background(), env, []string{arg}, registry)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if called {
				t.Fatal("help ran a command")
			}
			for _, want := range []string{"alpha", "first command", "beta", "second command"} {
				if !strings.Contains(stdout.String(), want) {
					t.Fatalf("stdout = %q, want %q", stdout.String(), want)
				}
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestMainNoArgs(t *testing.T) {
	env, stdout, stderr := testEnv()
	registry := mustRegistry(t, fakeCommand{name: "alpha", summary: "first command"})
	code := Main(context.Background(), env, nil, registry)
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "usage: repoctl") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestMainCommandErrorPrintsOnce(t *testing.T) {
	env, stdout, stderr := testEnv()
	registry := mustRegistry(t, fakeCommand{
		name:    "fail",
		summary: "always fails",
		run: func(context.Context, Env, []string) error {
			return errors.New("boom")
		},
	})
	code := Main(context.Background(), env, []string{"fail"}, registry)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := strings.Count(stderr.String(), "boom"); got != 1 {
		t.Fatalf("stderr = %q, want boom once", stderr.String())
	}
}

func TestMainFlagHelpExitsZero(t *testing.T) {
	env, _, stderr := testEnv()
	registry := mustRegistry(t, fakeCommand{
		name:    "helpful",
		summary: "returns flag.ErrHelp",
		run: func(context.Context, Env, []string) error {
			return flag.ErrHelp
		},
	})
	if code := Main(context.Background(), env, []string{"helpful", "--help"}, registry); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestNewRegistryRejectsInvalidCommands(t *testing.T) {
	if _, err := NewRegistry(fakeCommand{name: "same"}, fakeCommand{name: "same"}); err == nil {
		t.Fatal("duplicate names succeeded, want an error")
	}
	if _, err := NewRegistry(fakeCommand{name: " "}); err == nil {
		t.Fatal("empty name succeeded, want an error")
	}
	if _, err := NewRegistry(nil); err == nil {
		t.Fatal("nil command succeeded, want an error")
	}
}

func TestMainNilRegistry(t *testing.T) {
	env, _, stderr := testEnv()
	if code := Main(context.Background(), env, []string{"anything"}, nil); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "no command registry") {
		t.Fatalf("stderr = %q, want a registry error", stderr.String())
	}
}

func TestMainNilEnvFieldsDoNotPanic(t *testing.T) {
	registry := mustRegistry(t, fakeCommand{
		name:    "safe",
		summary: "uses every env field",
		run: func(_ context.Context, env Env, _ []string) error {
			_, _ = env.Stdout.Write([]byte("out"))
			_, _ = env.Stderr.Write([]byte("err"))
			if got := env.Getenv("MISSING"); got != "" {
				return errors.New("unexpected env value")
			}
			_ = env.Now()
			return nil
		},
	})
	if code := Main(context.Background(), Env{}, []string{"safe"}, registry); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestRegistryNamesSorted(t *testing.T) {
	registry := mustRegistry(t,
		fakeCommand{name: "zeta"},
		fakeCommand{name: "alpha"},
		fakeCommand{name: "mu"},
	)
	got := strings.Join(registry.Names(), ",")
	if got != "alpha,mu,zeta" {
		t.Fatalf("names = %q, want sorted", got)
	}
}
