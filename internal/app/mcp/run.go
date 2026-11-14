package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jawaracloud/skopos/internal/appenv"
	"github.com/jawaracloud/skopos/internal/version"
)

// Run starts the MCP server. It serves HTTP when SKOPOS_MCP_HTTP_ADDR is set
// and newline-delimited JSON-RPC over stdin and stdout otherwise.
func Run(ctx context.Context, env appenv.Env) error {
	env = env.Resolve()
	cfg := ConfigFromLookup(env.Lookup)
	cfg.Version = version.Version
	server, err := NewServer(cfg)
	if err != nil {
		return err
	}
	if cfg.HTTPAddr != "" {
		return serveHTTP(ctx, cfg.HTTPAddr, server, env.Stderr)
	}
	if err := server.Serve(env.Stdin, env.Stdout); err != nil {
		return err
	}
	return nil
}

func serveHTTP(ctx context.Context, addr string, server *Server, stderr io.Writer) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.HTTPHandler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	if _, err := fmt.Fprintf(stderr, "skopos: mcp listening on %s\n", addr); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}
