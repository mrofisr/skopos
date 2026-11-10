package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ClickHouse holds the accounting metrics for ClickHouse reads.
type ClickHouse struct {
	queries  *prometheus.CounterVec
	duration *prometheus.HistogramVec
	rows     *prometheus.CounterVec
	bytes    *prometheus.CounterVec
}

// NewClickHouse registers the ClickHouse metrics on registry.
func NewClickHouse(registry *Registry) *ClickHouse {
	clickhouse := &ClickHouse{
		queries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_clickhouse_queries_total",
			Help: "ClickHouse queries by head and result.",
		}, []string{"head", "result"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "skopos_clickhouse_query_duration_seconds",
			Help:    "ClickHouse query duration by head.",
			Buckets: prometheus.DefBuckets,
		}, []string{"head"}),
		rows: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_clickhouse_read_rows_total",
			Help: "Rows read from ClickHouse by head.",
		}, []string{"head"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "skopos_clickhouse_read_bytes_total",
			Help: "Bytes read from ClickHouse by head.",
		}, []string{"head"}),
	}
	registry.MustRegister(clickhouse.queries, clickhouse.duration, clickhouse.rows, clickhouse.bytes)
	return clickhouse
}

// QueryDone implements chclient.QueryObserver.
func (m *ClickHouse) QueryDone(head, result string, duration time.Duration, readRows, readBytes uint64) {
	m.queries.WithLabelValues(head, result).Inc()
	m.duration.WithLabelValues(head).Observe(duration.Seconds())
	if readRows > 0 {
		m.rows.WithLabelValues(head).Add(float64(readRows))
	}
	if readBytes > 0 {
		m.bytes.WithLabelValues(head).Add(float64(readBytes))
	}
}
