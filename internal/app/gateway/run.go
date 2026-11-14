// Package gateway serves the Skopos public API and query heads.
package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mrofisr/skopos/internal/appenv"
	"github.com/mrofisr/skopos/internal/auth"
	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/version"
)

// Run loads the gateway configuration and serves until ctx is cancelled.
func Run(ctx context.Context, env appenv.Env) error {
	cfg, err := config.FromLookup(env.Lookup)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(env.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	var basicAuth *auth.Basic
	if cfg.AuthEnabled {
		basicAuth, err = auth.New(cfg.AuthUsername, cfg.AuthPassword)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if cfg.Mode == config.ModeFrontend {
		return runFrontend(ctx, cfg, logger, basicAuth, version.Version)
	}
	return runQueries(ctx, cfg, logger, basicAuth, version.Version)
}
