package config

import (
	"strings"
	"testing"
	"time"
)

func TestConfig_RejectsExecutionTimeAboveQueryTimeout(t *testing.T) {
	tests := []struct {
		name         string
		maxExecution string
		queryTimeout string
		wantErr      string
		wantMax      time.Duration
		wantQuery    time.Duration
	}{
		{
			name:         "execution time above query timeout",
			maxExecution: "30s",
			queryTimeout: "10s",
			wantErr:      "SKOPOS_CLICKHOUSE_MAX_EXECUTION_TIME must not exceed SKOPOS_QUERY_TIMEOUT",
		},
		{
			name:         "equal values are accepted",
			maxExecution: "10s",
			queryTimeout: "10s",
			wantMax:      10 * time.Second,
			wantQuery:    10 * time.Second,
		},
		{
			name:         "execution time below query timeout",
			maxExecution: "5s",
			queryTimeout: "10s",
			wantMax:      5 * time.Second,
			wantQuery:    10 * time.Second,
		},
		{
			name:         "zero execution time",
			maxExecution: "0s",
			queryTimeout: "10s",
			wantErr:      "SKOPOS_CLICKHOUSE_MAX_EXECUTION_TIME: must be positive",
		},
		{
			name:         "negative execution time",
			maxExecution: "-1s",
			queryTimeout: "10s",
			wantErr:      "SKOPOS_CLICKHOUSE_MAX_EXECUTION_TIME: must be positive",
		},
		{
			name:         "zero query timeout",
			maxExecution: "5s",
			queryTimeout: "0s",
			wantErr:      "SKOPOS_QUERY_TIMEOUT: must be positive",
		},
		{
			name:         "negative query timeout",
			maxExecution: "5s",
			queryTimeout: "-1s",
			wantErr:      "SKOPOS_QUERY_TIMEOUT: must be positive",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := FromLookup(lookupFrom(map[string]string{
				"SKOPOS_CLICKHOUSE_MAX_EXECUTION_TIME": test.maxExecution,
				"SKOPOS_QUERY_TIMEOUT":                 test.queryTimeout,
			}))
			if test.wantErr != "" {
				if err == nil {
					t.Fatalf("FromLookup succeeded, want error containing %q", test.wantErr)
				}
				if !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %q, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("FromLookup: %v", err)
			}
			if cfg.MaxExecutionTime != test.wantMax || cfg.QueryTimeout != test.wantQuery {
				t.Fatalf("execution/query = %s/%s, want %s/%s",
					cfg.MaxExecutionTime, cfg.QueryTimeout, test.wantMax, test.wantQuery)
			}
		})
	}
}
