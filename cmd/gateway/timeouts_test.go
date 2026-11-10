package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/jawaracloud/skopos/internal/chclient"
	"github.com/jawaracloud/skopos/internal/config"
	"github.com/jawaracloud/skopos/internal/loki"
	"github.com/jawaracloud/skopos/internal/plan"
	"github.com/jawaracloud/skopos/internal/prom"
	"github.com/jawaracloud/skopos/internal/semctx"
	"github.com/jawaracloud/skopos/internal/tempo"
)

func TestServerTimeouts_DerivedFromQueryTimeout(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	for _, queryTimeout := range []time.Duration{
		time.Second,
		10 * time.Second,
		30 * time.Second,
		2 * time.Minute,
	} {
		srv := newServer(":0", http.NotFoundHandler(), logger, queryTimeout)
		if want := queryTimeout + 5*time.Second; srv.WriteTimeout != want {
			t.Errorf("query timeout %s: WriteTimeout = %s, want %s", queryTimeout, srv.WriteTimeout, want)
		}
		if srv.ReadHeaderTimeout != 5*time.Second {
			t.Errorf("query timeout %s: ReadHeaderTimeout = %s, want 5s", queryTimeout, srv.ReadHeaderTimeout)
		}
		if srv.ReadTimeout != 30*time.Second {
			t.Errorf("query timeout %s: ReadTimeout = %s, want 30s", queryTimeout, srv.ReadTimeout)
		}
		if srv.IdleTimeout != 120*time.Second {
			t.Errorf("query timeout %s: IdleTimeout = %s, want 120s", queryTimeout, srv.IdleTimeout)
		}
		if srv.MaxHeaderBytes != 1<<20 {
			t.Errorf("query timeout %s: MaxHeaderBytes = %d, want %d", queryTimeout, srv.MaxHeaderBytes, 1<<20)
		}
	}
}

func TestServerTimeouts_DefaultQueryTimeoutExceedsOldWriteTimeout(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	const defaultQueryTimeout = 30 * time.Second
	srv := newServer(":0", http.NotFoundHandler(), logger, defaultQueryTimeout)
	if srv.WriteTimeout <= defaultQueryTimeout {
		t.Fatalf("WriteTimeout = %s, want more than the %s query timeout", srv.WriteTimeout, defaultQueryTimeout)
	}
	if srv.WriteTimeout != 35*time.Second {
		t.Fatalf("WriteTimeout = %s, want 35s for the default 30s query timeout", srv.WriteTimeout)
	}
}

func TestNewMetricsRegistry(t *testing.T) {
	registry := newMetricsRegistry("test-version", nil)
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	if len(families) != 1 || families[0].GetName() != "skopos_build_info" {
		t.Fatalf("frontend families = %v, want only build info", families)
	}

	registry = newMetricsRegistry("test-version", semctx.NewAPI(semctx.EmptyCatalog()))
	families, err = registry.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := make(map[string]bool, len(families))
	for _, family := range families {
		names[family.GetName()] = true
	}
	if !names["skopos_build_info"] || !names["skopos_context_documented"] {
		t.Fatalf("context families = %v, want build info and documented gauges", names)
	}
}

func TestQueryAPIsUseConfiguredRangeAndTimeout(t *testing.T) {
	cfg := config.Config{
		MaxQueryRange: 7 * 24 * time.Hour,
		QueryTimeout:  12 * time.Second,
	}
	promAPI := prom.NewAPI(nil, nil, "test")
	tempoAPI := tempo.NewAPI(nil, "test")
	lokiAPI := loki.NewAPI(nil, "test")
	configureQueryAPIs(cfg, promAPI, tempoAPI, lokiAPI)
	if promAPI.MaxRange != cfg.MaxQueryRange || promAPI.Timeout != cfg.QueryTimeout {
		t.Fatalf("prom API = %s/%s", promAPI.MaxRange, promAPI.Timeout)
	}
	if tempoAPI.MaxRange != cfg.MaxQueryRange || tempoAPI.Timeout != cfg.QueryTimeout {
		t.Fatalf("tempo API = %s/%s", tempoAPI.MaxRange, tempoAPI.Timeout)
	}
	if lokiAPI.MaxRange != cfg.MaxQueryRange || lokiAPI.Timeout != cfg.QueryTimeout {
		t.Fatalf("loki API = %s/%s", lokiAPI.MaxRange, lokiAPI.Timeout)
	}
}

func TestNewClickHouseClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	client, err := newClickHouseClient(config.Config{
		ClickHouseURL:      "http://127.0.0.1:8123",
		ClickHouseDatabase: "otel",
		MaxExecutionTime:   30 * time.Second,
		MaxMemoryUsage:     1 << 30,
		MaxRowsToRead:      1000,
		MaxResultRows:      100,
		MaxResponseBytes:   1 << 20,
	}, nil, logger)
	if err != nil {
		t.Fatalf("newClickHouseClient: %v", err)
	}
	client.Close()

	if _, err := newClickHouseClient(config.Config{ClickHouseURL: "ftp://example.com"}, nil, logger); err == nil {
		t.Fatal("newClickHouseClient accepted an unsupported scheme")
	}
	if _, err := newClickHouseClient(config.Config{
		ClickHouseURL:    "http://127.0.0.1:8123",
		MaxResponseBytes: -1,
	}, nil, logger); err == nil {
		t.Fatal("newClickHouseClient accepted a negative response limit")
	}
}

func TestNewPromFetcher(t *testing.T) {
	client, err := chclient.New(chclient.Config{BaseURL: "http://127.0.0.1:8123"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tables := plan.DefaultTableNames()
	fetcher, err := newPromFetcher(client, plan.DefaultSchema("otel", tables), tables, 1000, time.Minute)
	if err != nil {
		t.Fatalf("newPromFetcher: %v", err)
	}
	if fetcher.MetadataTTL != time.Minute {
		t.Fatalf("MetadataTTL = %s, want 1m", fetcher.MetadataTTL)
	}
}
