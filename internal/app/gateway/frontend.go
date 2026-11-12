// This file composes the frontend role: it proxies to query workers and needs
// no ClickHouse credentials.
package gateway

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jawaracloud/skopos/internal/auth"
	"github.com/jawaracloud/skopos/internal/config"
	"github.com/jawaracloud/skopos/internal/frontend"
	"github.com/jawaracloud/skopos/internal/metrics"
	"github.com/jawaracloud/skopos/internal/semctx"
)

func runFrontend(ctx context.Context, cfg config.Config, logger *slog.Logger, basicAuth *auth.Basic, buildVersion string) error {
	registry := newMetricsRegistry(buildVersion, nil)
	httpMetrics := metrics.NewHTTP(registry)
	fe, err := frontend.New(frontend.Config{
		Logger:           logger,
		Backends:         cfg.QueryURLs,
		Version:          buildVersion,
		ReadinessTimeout: cfg.ReadinessTimeout,
		ProxyTimeout:     cfg.QueryTimeout,
		MaxRequestBytes:  cfg.MaxRequestBytes,
		Auth:             basicAuth,
		Metrics:          registry.Handler(),
	})
	if err != nil {
		return fmt.Errorf("frontend: %w", err)
	}

	queryURLs := make([]string, 0, len(cfg.QueryURLs))
	for _, raw := range cfg.QueryURLs {
		queryURLs = append(queryURLs, redactURL(raw))
	}
	logger.Info("starting gateway frontend",
		"version", buildVersion,
		"mode", string(cfg.Mode),
		"role", "frontend",
		"addr", cfg.ListenAddr,
		"query_urls", queryURLs,
		"query_timeout", cfg.QueryTimeout.String(),
		"readiness_timeout", cfg.ReadinessTimeout.String(),
		"auth_enabled", cfg.AuthEnabled,
	)

	return serve(ctx, cfg.ListenAddr, httpMetrics.Wrap(fe.Handler()), logger, cfg.QueryTimeout)
}

// newMetricsRegistry builds the shared registry. A nil context API is the
// frontend case: it has no catalog and exposes only build info.
func newMetricsRegistry(version string, contextAPI *semctx.API) *metrics.Registry {
	registry := metrics.NewRegistry(version)
	if contextAPI != nil {
		registry.MustRegister(metrics.NewContextCollector(contextAPI))
	}
	return registry
}
