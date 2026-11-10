package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/jawaracloud/skopos/internal/auth"
	"github.com/jawaracloud/skopos/internal/chclient"
	"github.com/jawaracloud/skopos/internal/config"
	"github.com/jawaracloud/skopos/internal/frontend"
	"github.com/jawaracloud/skopos/internal/httpapi"
	"github.com/jawaracloud/skopos/internal/loki"
	"github.com/jawaracloud/skopos/internal/metrics"
	"github.com/jawaracloud/skopos/internal/plan"
	"github.com/jawaracloud/skopos/internal/prom"
	"github.com/jawaracloud/skopos/internal/semctx"
	"github.com/jawaracloud/skopos/internal/tempo"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	var basicAuth *auth.Basic
	if cfg.AuthEnabled {
		basicAuth, err = auth.New(cfg.AuthUsername, cfg.AuthPassword)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	if cfg.Mode == config.ModeFrontend {
		return runFrontend(cfg, logger, basicAuth)
	}
	return runQueries(cfg, logger, basicAuth)
}

func runFrontend(cfg config.Config, logger *slog.Logger, basicAuth *auth.Basic) error {
	registry := newMetricsRegistry(version, nil)
	httpMetrics := metrics.NewHTTP(registry)
	fe, err := frontend.New(frontend.Config{
		Logger:           logger,
		Backends:         cfg.QueryURLs,
		Version:          version,
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
		"version", version,
		"mode", string(cfg.Mode),
		"role", "frontend",
		"addr", cfg.ListenAddr,
		"query_urls", queryURLs,
		"query_timeout", cfg.QueryTimeout.String(),
		"readiness_timeout", cfg.ReadinessTimeout.String(),
		"auth_enabled", cfg.AuthEnabled,
	)

	return serve(cfg.ListenAddr, httpMetrics.Wrap(fe.Handler()), logger, cfg.QueryTimeout)
}

func runQueries(cfg config.Config, logger *slog.Logger, basicAuth *auth.Basic) error {
	contextCatalog, err := semctx.LoadFile(cfg.ContextFile)
	if err != nil {
		return fmt.Errorf("semantic context: %w", err)
	}
	contextAPI := semctx.NewAPI(contextCatalog)
	registry := newMetricsRegistry(version, contextAPI)
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
	promAPI := prom.NewAPI(promEngine, fetcher, version)

	tempoFetcher, err := tempo.NewClickHouseFetcher(client, schema, cfg.Tables, cfg.MaxScanRows)
	if err != nil {
		return fmt.Errorf("tempo storage: %w", err)
	}
	tempoAPI := tempo.NewAPI(tempoFetcher, version)

	lokiFetcher, err := loki.NewClickHouseFetcher(client, schema, cfg.Tables, cfg.MaxScanRows)
	if err != nil {
		return fmt.Errorf("loki storage: %w", err)
	}
	lokiAPI := loki.NewAPI(lokiFetcher, version)
	configureQueryAPIs(cfg, promAPI, tempoAPI, lokiAPI)

	register := func(mux *http.ServeMux) {
		promAPI.Register(mux)
		tempoAPI.Register(mux)
		lokiAPI.Register(mux)
		contextAPI.Register(mux)
	}

	api, err := httpapi.New(httpapi.Config{
		Logger:           logger,
		Pinger:           client,
		Version:          version,
		ReadinessTimeout: cfg.ReadinessTimeout,
		MaxRequestBytes:  cfg.MaxRequestBytes,
		Auth:             basicAuth,
		Register:         register,
		Metrics:          registry.Handler(),
		Profile:          cfg.Profile,
	})
	if err != nil {
		return fmt.Errorf("http api: %w", err)
	}

	attrs := []any{
		"version", version,
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

	handler := api.Handler()
	if admission != nil {
		handler = admission.Wrap(handler)
	}
	return serve(cfg.ListenAddr, httpMetrics.Wrap(handler), logger, cfg.QueryTimeout)
}

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

func serve(addr string, handler http.Handler, logger *slog.Logger, queryTimeout time.Duration) error {
	srv := newServer(addr, handler, logger, queryTimeout)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
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
