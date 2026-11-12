package appenv

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestResolveFillsDefaultsWithoutOverriding(t *testing.T) {
	var stdout bytes.Buffer
	env := Env{Args: []string{"version"}, Stdout: &stdout}
	resolved := env.Resolve()
	if resolved.Stdin == nil || resolved.Stderr == nil || resolved.LookupEnv == nil {
		t.Fatalf("Resolve left a field unset: %#v", resolved)
	}
	if resolved.Stdout != &stdout {
		t.Fatal("Resolve replaced a caller-supplied writer")
	}
	if len(resolved.Args) != 1 || resolved.Args[0] != "version" {
		t.Fatalf("Args = %#v", resolved.Args)
	}
}

func TestLookupAndGetenv(t *testing.T) {
	env := Env{LookupEnv: func(key string) (string, bool) {
		if key == "SET" {
			return "value", true
		}
		return "", false
	}}
	if value, ok := env.Lookup("SET"); !ok || value != "value" {
		t.Fatalf("Lookup(SET) = %q, %v", value, ok)
	}
	if value, ok := env.Lookup("UNSET"); ok || value != "" {
		t.Fatalf("Lookup(UNSET) = %q, %v", value, ok)
	}
	if got := env.Getenv("SET"); got != "value" {
		t.Fatalf("Getenv(SET) = %q", got)
	}
	if got := env.Getenv("UNSET"); got != "" {
		t.Fatalf("Getenv(UNSET) = %q", got)
	}
}

func TestZeroEnvDoesNotPanic(t *testing.T) {
	var env Env
	if value, ok := env.Lookup("HOME"); ok || value != "" {
		t.Fatalf("zero Lookup = %q, %v", value, ok)
	}
	if got := env.Getenv("HOME"); got != "" {
		t.Fatalf("zero Getenv = %q", got)
	}
}

func TestOSReadsProcessEnvironment(t *testing.T) {
	t.Setenv("SKOPOS_APPENV_TEST", "1")
	env := OS()
	if env.Stdin == nil || env.Stdout == nil || env.Stderr == nil || env.LookupEnv == nil {
		t.Fatalf("OS left a field unset: %#v", env)
	}
	if got, ok := env.Lookup("SKOPOS_APPENV_TEST"); !ok || got != "1" {
		t.Fatalf("Lookup(SKOPOS_APPENV_TEST) = %q, %v", got, ok)
	}
	if env.Getenv("SKOPOS_APPENV_TEST") != "1" {
		t.Fatal("Getenv did not read the process environment")
	}
	if strings.Join(env.Args, " ") != strings.Join(os.Args[1:], " ") {
		t.Fatalf("Args = %#v, want process arguments", env.Args)
	}
}
