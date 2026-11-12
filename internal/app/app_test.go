package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jawaracloud/skopos/internal/appenv"
	"github.com/jawaracloud/skopos/internal/version"
)

func testEnv(args []string, env map[string]string) (appenv.Env, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return appenv.Env{
		Args:   args,
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		LookupEnv: func(key string) (string, bool) {
			value, ok := env[key]
			return value, ok
		},
	}, &stdout, &stderr
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		raw  string
		want Mode
	}{
		{"", ModeAll},
		{"all", ModeAll},
		{"ALL", ModeAll},
		{"gateway", ModeGateway},
		{" query ", ModeQuery},
		{"worker", ModeWorker},
		{"frontend", ModeFrontend},
		{"mcp", ModeMCP},
		{"CHECKPOINT", ModeCheckpoint},
	}
	for _, test := range tests {
		got, err := ParseMode(test.raw)
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", test.raw, err)
		}
		if got != test.want {
			t.Fatalf("ParseMode(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
	if _, err := ParseMode("backend"); err == nil || !strings.Contains(err.Error(), "SKOPOS_MODE") {
		t.Fatalf("ParseMode(backend) error = %v", err)
	}
}

func TestModeFromEnvDefaultsToAll(t *testing.T) {
	env, _, _ := testEnv(nil, nil)
	mode, err := ModeFromEnv(env)
	if err != nil {
		t.Fatal(err)
	}
	if mode != ModeAll {
		t.Fatalf("mode = %q, want %q", mode, ModeAll)
	}
}

func TestRunPrintsVersion(t *testing.T) {
	previous := version.Version
	version.Version = "1.2.3"
	t.Cleanup(func() { version.Version = previous })

	env, stdout, _ := testEnv([]string{"version"}, nil)
	if err := Run(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "1.2.3\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestRunPrintsUsage(t *testing.T) {
	env, stdout, _ := testEnv([]string{"--help"}, nil)
	if err := Run(context.Background(), env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "SKOPOS_MODE selects") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestMainReportsAnUnknownModeOnce(t *testing.T) {
	env, _, stderr := testEnv(nil, map[string]string{"SKOPOS_MODE": "backend"})
	if code := Main(context.Background(), env); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	got := stderr.String()
	if strings.Count(got, "SKOPOS_MODE") != 1 || !strings.HasPrefix(got, "skopos: ") {
		t.Fatalf("stderr = %q", got)
	}
}
