// Command scale-gen writes a deterministic synthetic dataset for the scale
// profiles.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mrofisr/skopos/test/scale"
)

func main() {
	if err := scale.Run(context.Background(), scale.Env{Getenv: os.Getenv, Now: time.Now}, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "scale-gen: %v\n", err)
		os.Exit(1)
	}
}
