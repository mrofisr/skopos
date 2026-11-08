// Command skopos-osv-check validates the dated OSV exception ledger. The
// release preflight calls it through scripts/check-osv-exceptions.sh.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jawaracloud/skopos/internal/osvexceptions"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "check-osv-exceptions: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("check-osv-exceptions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	file := flags.String("file", "docs/security/osv-exceptions.yaml", "exception ledger")
	nowFlag := flags.String("now", "", "evaluation date (YYYY-MM-DD); defaults to today")
	if err := flags.Parse(args); err != nil {
		return err
	}

	now := time.Now().UTC()
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
	ledger, err := osvexceptions.Parse(data)
	if err != nil {
		return err
	}
	if err := osvexceptions.Validate(ledger, now); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "check-osv-exceptions: %d exceptions, 0 expired\n", len(ledger.Exceptions))
	return nil
}
