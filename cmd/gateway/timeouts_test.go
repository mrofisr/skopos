package main

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
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
