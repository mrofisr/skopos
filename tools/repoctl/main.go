// Command repoctl runs the Skopos repository checks.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/mrofisr/skopos/tools/repoctl/internal/command"
)

func main() {
	registry, err := newRegistry()
	if err != nil {
		fmt.Fprintln(os.Stderr, "repoctl:", err)
		os.Exit(2)
	}
	os.Exit(command.Main(context.Background(), command.OSEnv(), os.Args[1:], registry))
}
