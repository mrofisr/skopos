// Command skopos is the single Skopos binary. SKOPOS_MODE selects the role:
// all (default), gateway, query, worker, frontend, mcp, or checkpoint.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/jawaracloud/skopos/internal/app"
	"github.com/jawaracloud/skopos/internal/appenv"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.Main(ctx, appenv.OS()))
}
