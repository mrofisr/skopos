//go:build integration

package prom

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mrofisr/skopos/internal/chclient"
	"github.com/mrofisr/skopos/internal/plan"
)

type sampleOnlyFetcher struct {
	fetcher *ClickHouseFetcher
}

func (s sampleOnlyFetcher) Fetch(ctx context.Context, sel Selector, start, end time.Time) ([]Series, error) {
	return s.fetcher.Fetch(ctx, sel, start, end)
}

type clickHouseAdmin struct {
	t        *testing.T
	baseURL  string
	user     string
	password string
	client   *http.Client
}

func newClickHouseAdmin(t *testing.T) *clickHouseAdmin {
	t.Helper()
	baseURL := os.Getenv("CLICKHOUSE_URL")
	if baseURL == "" {
		t.Fatal("CLICKHOUSE_URL is required for integration tests")
	}
	return &clickHouseAdmin{
		t:        t,
		baseURL:  baseURL,
		user:     os.Getenv("CLICKHOUSE_USER"),
		password: os.Getenv("CLICKHOUSE_PASSWORD"),
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

func (a *clickHouseAdmin) exec(query string) string {
	a.t.Helper()
	req, err := http.NewRequest(http.MethodPost, a.baseURL, strings.NewReader(query))
	if err != nil {
		a.t.Fatalf("build request: %v", err)
	}
	if a.user != "" {
		req.SetBasicAuth(a.user, a.password)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		a.t.Fatalf("query failed: status %d: %s\nquery: %s", resp.StatusCode, body, query)
	}
	return strings.TrimSpace(string(body))
}

func newRateFetcher(t *testing.T, admin *clickHouseAdmin, database string) *ClickHouseFetcher {
	t.Helper()
	client, err := chclient.New(chclient.Config{
		BaseURL:  admin.baseURL,
		Database: database,
		User:     admin.user,
		Password: admin.password,
	})
	if err != nil {
		t.Fatalf("chclient.New: %v", err)
	}
	names := plan.DefaultTableNames()
	fetcher, err := NewClickHouseFetcher(client, plan.DefaultSchema(database, names), names, 200_000)
	if err != nil {
		t.Fatalf("NewClickHouseFetcher: %v", err)
	}
	return fetcher
}

// createMetricsTables creates the five metrics tables the discovery probe
// expects. Tests that only populate one table still need the others to exist.
func createMetricsTables(admin *clickHouseAdmin, database string) {
	columns := "MetricName LowCardinality(String), TimeUnix DateTime64(9), Value Float64, " +
		"ServiceName LowCardinality(String), Attributes Map(String, String)) "
	for _, table := range []string{"otel_metrics_sum", "otel_metrics_gauge", "otel_metrics_histogram", "otel_metrics_summary", "otel_metrics_exponential_histogram"} {
		admin.exec("CREATE TABLE `" + database + "`.`" + table + "` (" + columns +
			"ENGINE = MergeTree ORDER BY (MetricName, TimeUnix)")
	}
}

func TestIntegrationRatePushdownParity(t *testing.T) {
	admin := newClickHouseAdmin(t)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	database := "skopos_rate_it_" + suffix
	admin.exec("CREATE DATABASE `" + database + "`")
	t.Cleanup(func() { admin.exec("DROP DATABASE IF EXISTS `" + database + "` SYNC") })
	createMetricsTables(admin, database)

	rows := strings.Join([]string{
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":5,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":15,"ServiceName":"service-a","Attributes":{"service.name":"service-a"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":30,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":40,"ServiceName":"service-b","Attributes":{"service.name":"service-b"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","Value":10,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:01:00.000000000","Value":20,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:02:00.000000000","Value":5,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
		`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:03:00.000000000","Value":15,"ServiceName":"","Attributes":{"service.name":"service-c"}}`,
	}, "\n")
	admin.exec("INSERT INTO `" + database + "`.`otel_metrics_sum` FORMAT JSONEachRow\n" + rows)

	fetcher := newRateFetcher(t, admin, database)
	fast := NewEngine(fetcher)
	sample := NewEngine(sampleOnlyFetcher{fetcher: fetcher})
	at := time.Date(2026, 11, 15, 12, 4, 0, 0, time.UTC)

	for _, query := range []string{
		"sum(rate(http_requests_total[5m]))",
		"sum by (service_name) (rate(http_requests_total[5m]))",
	} {
		fastValue, err := fast.Query(context.Background(), query, at)
		if err != nil {
			t.Fatalf("%s fast: %v", query, err)
		}
		sampleValue, err := sample.Query(context.Background(), query, at)
		if err != nil {
			t.Fatalf("%s sample: %v", query, err)
		}
		assertVectorsEqual(t, query, fastValue.(Vector), sampleValue.(Vector))
	}
}

// TestIntegrationRatePushdownRowBound pins the primary-key pruning the
// five-minute sort key is meant to buy. The fixture is deliberately sparse
// (one series, 1000 samples per bucket) and omits the TimeUnix minmax index so
// only the primary key can prune; the assertion is therefore deterministic in
// PR rather than a timing measurement.
func TestIntegrationRatePushdownRowBound(t *testing.T) {
	admin := newClickHouseAdmin(t)
	database := fmt.Sprintf("skopos_rate_bound_%d", time.Now().UnixNano())
	admin.exec("CREATE DATABASE `" + database + "`")
	t.Cleanup(func() { admin.exec("DROP DATABASE IF EXISTS `" + database + "` SYNC") })

	columns := "MetricName LowCardinality(String), ServiceName LowCardinality(String), " +
		"Attributes Map(LowCardinality(String), String), TimeUnix DateTime64(9), Value Float64, " +
		"`_skopos_row_hash` FixedString(16) MATERIALIZED sipHash128(MetricName, ServiceName, mapSort(Attributes), TimeUnix, Value)"
	for _, table := range []string{"otel_metrics_gauge", "otel_metrics_histogram", "otel_metrics_summary", "otel_metrics_exponential_histogram"} {
		admin.exec("CREATE TABLE `" + database + "`.`" + table + "` (" + columns + ") " +
			"ENGINE = ReplacingMergeTree ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix, _skopos_row_hash)")
	}
	admin.exec("CREATE TABLE `" + database + "`.`otel_metrics_sum` (" + columns + ") " +
		"ENGINE = ReplacingMergeTree ORDER BY (toStartOfFiveMinutes(TimeUnix), MetricName, ServiceName, cityHash64(mapSort(Attributes)), TimeUnix, _skopos_row_hash) " +
		"SETTINGS index_granularity = 8192")
	// 48 buckets of 1000 samples, 300ms apart. A raw TimeUnix range has to
	// scan every granule; the bucket predicate can skip to two of them.
	admin.exec("INSERT INTO `" + database + "`.`otel_metrics_sum` " +
		"SELECT 'http_requests_total','svc',map('service.name','svc')," +
		"toDateTime64('2026-11-15 00:00:00',9) + toIntervalMillisecond(intDiv(number,1000)*300000 + (number%1000)*300)," +
		"toFloat64(number) FROM numbers(48000)")

	fetcher := newRateFetcher(t, admin, database)
	recorder := &chclient.Recorder{}
	ctx := chclient.WithRecorder(context.Background(), recorder)
	at := time.Date(2026, 11, 15, 0, 27, 30, 0, time.UTC)
	if _, err := NewEngine(fetcher).Query(ctx, "sum(rate(http_requests_total[5m]))", at); err != nil {
		t.Fatalf("Query: %v", err)
	}

	var rate *chclient.QueryRecord
	for _, record := range recorder.Statements() {
		if strings.Contains(record.SQL, "lagInFrame") {
			r := record
			rate = &r
			break
		}
	}
	if rate == nil {
		t.Fatalf("no rate query recorded: %+v", recorder.Statements())
	}
	if !strings.Contains(rate.SQL, "toStartOfFiveMinutes") {
		t.Fatalf("rate query has no bucket predicate:\n%s", rate.SQL)
	}
	// The two touched buckets hold 2000 rows in one 8192-row granule; two
	// granules is the tight bound. A raw TimeUnix scan reads all 48,000.
	const bound = 2 * 8192
	if rate.ReadRows > bound {
		t.Fatalf("rate query read %d rows, want <= %d", rate.ReadRows, bound)
	}
	t.Logf("rate query read %d rows", rate.ReadRows)
}

func assertVectorsEqual(t *testing.T, name string, got, want Vector) {
	t.Helper()
	sort.Slice(got, func(i, j int) bool { return labelsKey(got[i].Labels) < labelsKey(got[j].Labels) })
	sort.Slice(want, func(i, j int) bool { return labelsKey(want[i].Labels) < labelsKey(want[j].Labels) })
	if len(got) != len(want) {
		t.Fatalf("%s: got %d samples, want %d (%#v vs %#v)", name, len(got), len(want), got, want)
	}
	for i := range got {
		if labelsKey(got[i].Labels) != labelsKey(want[i].Labels) {
			t.Fatalf("%s: labels %v, want %v", name, got[i].Labels, want[i].Labels)
		}
		scale := math.Max(1, math.Max(math.Abs(got[i].V), math.Abs(want[i].V)))
		if math.Abs(got[i].V-want[i].V) > 1e-9*scale {
			t.Fatalf("%s: value %v, want %v", name, got[i].V, want[i].V)
		}
	}
}
