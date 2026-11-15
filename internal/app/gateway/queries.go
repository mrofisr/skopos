// This file composes the query role: it wires ClickHouse, the three query
// heads, the context API, admission control, and the public handler.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/mrofisr/skopos/internal/auth"
	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/config"
	"github.com/mrofisr/skopos/internal/httpapi"
	"github.com/mrofisr/skopos/internal/loki"
	"github.com/mrofisr/skopos/internal/metrics"
	"github.com/mrofisr/skopos/internal/plan"
	"github.com/mrofisr/skopos/internal/prom"
	"github.com/mrofisr/skopos/internal/semctx"
	"github.com/mrofisr/skopos/internal/tempo"
)

func runQueries(ctx context.Context, cfg config.Config, logger *slog.Logger, basicAuth *auth.Basic, buildVersion string) error {
	contextCatalog, err := semctx.LoadFile(cfg.ContextFile)
	if err != nil {
		return fmt.Errorf("semantic context: %w", err)
	}
	contextAPI := semctx.NewAPI(contextCatalog)
	registry := newMetricsRegistry(buildVersion, contextAPI)
	clickHouseMetrics := metrics.NewClickHouse(registry)

	client, err := newClickHouseClient(cfg, clickHouseMetrics, logger)
	if err != nil {
		return fmt.Errorf("clickhouse client: %w", err)
	}
	defer client.Close()

	schema := plan.DefaultSchema(cfg.ClickHouseDatabase, cfg.Tables)
	fetcher, err := newPromFetcher(client, schema, cfg.Tables, cfg.MaxScanRows, cfg.MetadataTTL)
	if err != nil {
		return fmt.Errorf("prometheus storage: %w", err)
	}
	if cfg.ContextObserved {
		contextAPI.WithObserver(newContextObserver(fetcher, logger))
		contextAPI.WithSignalObserver(newObservedSignals(client, cfg.ClickHouseDatabase, cfg.Tables, logger))
	}
	httpMetrics := metrics.NewHTTP(registry)
	admission, err := newAdmissionMiddleware(cfg, httpMetrics)
	if err != nil {
		return fmt.Errorf("admission: %w", err)
	}
	if warning := worstCaseBufferingWarning(cfg, debug.SetMemoryLimit(-1)); warning != "" {
		logger.Warn(warning)
	}
	promEngine := prom.NewEngine(fetcher)
	promEngine.MaxSamples = cfg.MaxScanRows
	promAPI := prom.NewAPI(promEngine, fetcher, buildVersion)

	tempoFetcher, err := tempo.NewClickHouseFetcher(client, schema, cfg.Tables, cfg.MaxScanRows)
	if err != nil {
		return fmt.Errorf("tempo storage: %w", err)
	}
	tempoAPI := tempo.NewAPI(tempoFetcher, buildVersion)

	lokiFetcher, err := loki.NewClickHouseFetcher(client, schema, cfg.Tables, cfg.MaxScanRows)
	if err != nil {
		return fmt.Errorf("loki storage: %w", err)
	}
	lokiAPI := loki.NewAPI(lokiFetcher, buildVersion)
	configureQueryAPIs(cfg, promAPI, tempoAPI, lokiAPI)

	register := func(mux *http.ServeMux) {
		promAPI.Register(mux)
		tempoAPI.Register(mux)
		lokiAPI.Register(mux)
		contextAPI.Register(mux)
	}

	api, err := httpapi.New(httpapi.Config{
		Logger:             logger,
		Pinger:             client,
		Version:            buildVersion,
		ReadinessTimeout:   cfg.ReadinessTimeout,
		MaxRequestBytes:    cfg.MaxRequestBytes,
		Auth:               basicAuth,
		Register:           register,
		Metrics:            registry.Handler(),
		Profile:            cfg.Profile,
		ProfileAllowUnauth: cfg.ProfileAllowUnauth,
	})
	if err != nil {
		return fmt.Errorf("http api: %w", err)
	}

	attrs := []any{
		"version", buildVersion,
		"mode", string(cfg.Mode),
		"addr", cfg.ListenAddr,
		"clickhouse_url", redactURL(cfg.ClickHouseURL),
		"database", cfg.ClickHouseDatabase,
		"read_only_user", cfg.ClickHouseUser,
		"tables", cfg.Tables,
		"promql", true,
		"traceql", true,
		"logql", true,
		"auth_enabled", cfg.AuthEnabled,
		"context_metrics", len(contextCatalog.Metrics),
		"context_services", len(contextCatalog.Services),
		"context_logs", len(contextCatalog.Logs),
		"context_traces", len(contextCatalog.Traces),
		"context_observed", cfg.ContextObserved,
	}
	switch cfg.Mode {
	case config.ModeWorker:
		// SKOPOS_MODE=worker is the documented alias for the query worker role.
		logger.Info("starting query worker (SKOPOS_MODE=worker is an alias for query)", attrs...)
	case config.ModeQuery:
		logger.Info("starting query worker", attrs...)
	default:
		logger.Info("starting gateway", attrs...)
	}

	return serve(ctx, cfg.ListenAddr, httpMetrics.Wrap(queryHandler(api, admission)), logger, cfg.QueryTimeout)
}

// queryHandler builds the public query handler. The auth guard lives inside the
// HTTP API; admission is inserted under it so unauthenticated traffic cannot
// occupy slots.
func queryHandler(api *httpapi.Server, admission *admissionMiddleware) http.Handler {
	return api.HandlerWith(func(next http.Handler) http.Handler {
		if admission == nil {
			return next
		}
		return admission.Wrap(next)
	})
}

// configureQueryAPIs applies the shared range and per-request deadline to the
// three query heads.
func configureQueryAPIs(cfg config.Config, promAPI *prom.API, tempoAPI *tempo.API, lokiAPI *loki.API) {
	promAPI.MaxRange = cfg.MaxQueryRange
	promAPI.Timeout = cfg.QueryTimeout
	tempoAPI.MaxRange = cfg.MaxQueryRange
	tempoAPI.Timeout = cfg.QueryTimeout
	lokiAPI.MaxRange = cfg.MaxQueryRange
	lokiAPI.Timeout = cfg.QueryTimeout
}

// newClickHouseClient builds the read-only ClickHouse client from gateway
// config.
func newClickHouseClient(cfg config.Config, observer chclient.QueryObserver, logger *slog.Logger) (*chclient.Client, error) {
	return chclient.New(chclient.Config{
		BaseURL:          cfg.ClickHouseURL,
		Database:         cfg.ClickHouseDatabase,
		User:             cfg.ClickHouseUser,
		Password:         cfg.ClickHousePassword,
		Timeout:          cfg.MaxExecutionTime,
		MaxExecutionTime: cfg.MaxExecutionTime,
		MaxMemoryUsage:   cfg.MaxMemoryUsage,
		MaxRowsToRead:    cfg.MaxRowsToRead,
		MaxResultRows:    cfg.MaxResultRows,
		MaxResponseBytes: cfg.MaxResponseBytes,
		Observer:         observer,
		Logger:           logger,
	})
}

// newPromFetcher builds the Prometheus storage fetcher and applies the metadata
// cache TTL.
func newPromFetcher(client *chclient.Client, schema *plan.Schema, tables plan.TableNames, maxScanRows int, metadataTTL time.Duration) (*prom.ClickHouseFetcher, error) {
	fetcher, err := prom.NewClickHouseFetcher(client, schema, tables, maxScanRows)
	if err != nil {
		return nil, err
	}
	fetcher.MetadataTTL = metadataTTL
	return fetcher, nil
}
