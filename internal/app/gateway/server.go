// This file owns the HTTP server lifecycle: construction, listen, and graceful
// shutdown on context cancellation.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// newServer builds the HTTP server. WriteTimeout is derived from the query
// timeout plus a margin for encoding and writing the response, so a query that
// runs to its deadline still finishes instead of being reset mid-response.
func newServer(addr string, handler http.Handler, logger *slog.Logger, queryTimeout time.Duration) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      queryTimeout + 5*time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}

func serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger, queryTimeout time.Duration) error {
	srv := newServer(addr, handler, logger, queryTimeout)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
