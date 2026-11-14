// Package appenv carries the process environment into the application modes.
//
// Applications accept an Env instead of reading os.Args, os.Stdin, os.Stdout,
// os.Stderr or os.Getenv directly. That keeps the composition root in
// cmd/skopos small and lets every mode be exercised in tests with fake inputs.
package appenv

import (
	"io"
	"os"
)

// Env is the process environment for one Skopos invocation.
type Env struct {
	// Args are the command-line arguments after the program name.
	Args []string
	// Stdin is the process standard input.
	Stdin io.Reader
	// Stdout is the process standard output.
	Stdout io.Writer
	// Stderr is the process standard error.
	Stderr io.Writer
	// LookupEnv resolves an environment variable. A nil value reports that the
	// variable is unset.
	LookupEnv func(string) (string, bool)
}

// OS returns the environment of the running process.
func OS() Env {
	return Env{
		Args:      os.Args[1:],
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		LookupEnv: os.LookupEnv,
	}
}

// Resolve fills unset fields with the process defaults. It never overrides a
// value supplied by the caller.
func (e Env) Resolve() Env {
	if e.Stdin == nil {
		e.Stdin = os.Stdin
	}
	if e.Stdout == nil {
		e.Stdout = os.Stdout
	}
	if e.Stderr == nil {
		e.Stderr = os.Stderr
	}
	if e.LookupEnv == nil {
		e.LookupEnv = os.LookupEnv
	}
	return e
}

// Lookup resolves one environment variable.
func (e Env) Lookup(key string) (string, bool) {
	if e.LookupEnv == nil {
		return "", false
	}
	return e.LookupEnv(key)
}

// Getenv resolves one environment variable, returning the empty string when it
// is unset.
func (e Env) Getenv(key string) string {
	value, _ := e.Lookup(key)
	return value
}
