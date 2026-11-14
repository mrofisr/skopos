package osvexceptions

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

// Command implements `repoctl osv`.
type Command struct{}

// Name implements command.Command.
func (Command) Name() string { return "osv" }

// Summary implements command.Command.
func (Command) Summary() string { return "validate the dated OSV exception ledger" }

// Run implements command.Command.
func (Command) Run(_ context.Context, env command.Env, args []string) error {
	fs := flag.NewFlagSet("osv", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	file := fs.String("file", "docs/security/osv-exceptions.yaml", "exception ledger")
	nowFlag := fs.String("now", "", "evaluation date (YYYY-MM-DD); defaults to today")
	if err := fs.Parse(args); err != nil {
		return err
	}

	now := env.Now().UTC()
	if *nowFlag != "" {
		parsed, err := time.Parse("2006-01-02", *nowFlag)
		if err != nil {
			return fmt.Errorf("invalid --now %q: expected YYYY-MM-DD", *nowFlag)
		}
		now = parsed
	}

	data, err := os.ReadFile(*file)
	if err != nil {
		return fmt.Errorf("read ledger: %w", err)
	}
	ledger, err := Parse(data)
	if err != nil {
		return err
	}
	if err := Validate(ledger, now); err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "osv: %d exceptions, 0 expired\n", len(ledger.Exceptions))
	return nil
}
