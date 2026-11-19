//go:build integration

package migrate

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests exercise the chart's migrate.sh against a real ClickHouse over
// HTTP. clickhouse-client is replaced by a small shim so the same script runs
// in CI without the client binary.

const shim = `#!/bin/sh
set -eu
query=''
multi=''
while [ $# -gt 0 ]; do
  case "$1" in
    --query) query="$2"; shift 2 ;;
    --multiquery) multi=1; shift ;;
    --host|--port|--user|--password) shift 2 ;;
    *) shift ;;
  esac
done
post() { curl -fsS "${CLICKHOUSE_URL}/" --data-binary "$1"; }
if [ -n "$query" ]; then post "$query"; exit 0; fi
if [ -n "$multi" ]; then
  sed '/^[[:space:]]*--/d' | awk 'BEGIN { RS = ";" } {
    gsub(/\n/, " "); gsub(/^[ \t]+|[ \t]+$/, "")
    if (length($0) > 0) print
  }' | while IFS= read -r stmt; do post "$stmt" >/dev/null; done
  exit 0
fi
exit 0
`

var (
	storageBlock = regexp.MustCompile(`(?s)\{\{- if \.Values\.clickhouse\.storage\.enabled \}\}.*?\{\{- else \}\}(.*?)\{\{- end \}\}`)
	dedupBlock   = regexp.MustCompile(`(?ms)^\{\{ if \.Values\.clickhouse\.dedup\.enabled \}\}\n(.*?)^\{\{ end \}\}\n?`)
	oldKey       = regexp.MustCompile(`ORDER BY \(toStartOfFiveMinutes\(TimeUnix\), MetricName, ServiceName, cityHash64\(mapSort\(Attributes\)\), TimeUnix(, _skopos_row_hash)?\)`)
)

type clickhouse struct {
	base string
}

func (c clickhouse) try(query string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, c.base, strings.NewReader(query))
	if err != nil {
		return "", err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return strings.TrimSpace(string(body)), nil
}

func (c clickhouse) exec(t *testing.T, query string) string {
	t.Helper()
	got, err := c.try(query)
	if err != nil {
		t.Fatalf("query failed: %v\nquery: %s", err, query)
	}
	return got
}

func renderSchema(t *testing.T, raw, database string, dedup bool) string {
	t.Helper()
	// Remove the inline storage_policy fragment before matching the block form.
	// Otherwise an inline {{- if }} can pair with the next table's {{- else }}.
	rendered := strings.ReplaceAll(raw,
		"{{- if .Values.clickhouse.storage.enabled }}, storage_policy = 'tiered'{{- end }}", "")
	rendered = storageBlock.ReplaceAllString(rendered, "$1")
	if dedup {
		rendered = dedupBlock.ReplaceAllString(rendered, "$1")
	} else {
		rendered = dedupBlock.ReplaceAllString(rendered, "")
	}
	engine := "MergeTree()"
	hash := ""
	if dedup {
		engine = "ReplacingMergeTree()"
		hash = ", _skopos_row_hash"
	}
	rendered = strings.ReplaceAll(rendered,
		"{{ if .Values.clickhouse.dedup.enabled }}ReplacingMergeTree(){{ else }}MergeTree(){{ end }}", engine)
	rendered = strings.ReplaceAll(rendered,
		"{{ if .Values.clickhouse.dedup.enabled }}, _skopos_row_hash{{ end }}", hash)
	rendered = strings.NewReplacer(
		"{{ .Values.clickhouse.database }}", database,
		"{{ .Values.clickhouse.tables.traces }}", "otel_traces",
		"{{ .Values.clickhouse.tables.logs }}", "otel_logs",
		"{{ .Values.clickhouse.tables.metricsGauge }}", "otel_metrics_gauge",
		"{{ .Values.clickhouse.tables.metricsSum }}", "otel_metrics_sum",
		"{{ .Values.clickhouse.tables.metricsHistogram }}", "otel_metrics_histogram",
		"{{ .Values.clickhouse.tables.metricsExponentialHistogram }}", "otel_metrics_exponential_histogram",
		"{{ .Values.clickhouse.tables.metricsSummary }}", "otel_metrics_summary",
		"{{ .Values.clickhouse.ttl.tracesDays }}", "20000",
		"{{ .Values.clickhouse.ttl.logsDays }}", "20000",
		"{{ .Values.clickhouse.ttl.metricsDays }}", "20000",
		"{{ .Values.clickhouse.storage.hotDays }}", "1",
		"{{ .Values.clickhouse.storage.totalDays }}", "20000",
		"{{ .Values.clickhouse.storage.coldVolume }}", "cold",
	).Replace(rendered)
	if strings.Contains(rendered, "{{") {
		t.Fatalf("rendered schema still contains a placeholder")
	}
	return rendered
}

// applyStatements splits rendered DDL and applies it over HTTP.
func applyStatements(t *testing.T, ch clickhouse, schema string) {
	t.Helper()
	lines := make([]string, 0, 64)
	for _, line := range strings.Split(schema, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		lines = append(lines, line)
	}
	for _, statement := range strings.Split(strings.Join(lines, "\n"), ";") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		ch.exec(t, statement)
	}
}

// oldSchema rewrites the current bucket key back to the pre-change hour key.
// Before the implementation this is a no-op, which is exactly what makes the
// migration tests fail first.
func oldSchema(t *testing.T, schema string) string {
	t.Helper()
	return oldKey.ReplaceAllString(schema,
		"ORDER BY (ServiceName, MetricName, toStartOfHour(TimeUnix), cityHash64(mapSort(Attributes)), TimeUnix$1)")
}

type harness struct {
	t        *testing.T
	ch       clickhouse
	database string
	script   string
	shimDir  string
}

func newHarness(t *testing.T, dedup bool) *harness {
	t.Helper()
	return newHarnessProfile(t, dedup, dedup)
}

// newHarnessProfile seeds the database with the fixture profile and migrates
// with the target profile, so the pre-dedup upgrade path (MergeTree fixture to
// ReplacingMergeTree target) is covered as well.
func newHarnessProfile(t *testing.T, dedup, fixtureDedup bool) *harness {
	t.Helper()
	base := os.Getenv("CLICKHOUSE_URL")
	if base == "" {
		t.Fatal("CLICKHOUSE_URL is required for integration tests")
	}
	root := filepath.Join("..", "..", "..")
	schemaRaw, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "skopos", "files", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	scriptRaw, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "skopos", "files", "migrate.sh"))
	if err != nil {
		t.Fatalf("read migrate.sh: %v", err)
	}
	database := fmt.Sprintf("skopos_migrate_%d", time.Now().UnixNano())
	ch := clickhouse{base: base}

	dir := t.TempDir()
	shimDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatalf("create shim dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(shimDir, "clickhouse-client"), []byte(shim), 0o755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	schema := renderSchema(t, string(schemaRaw), database, dedup)
	schemaPath := filepath.Join(dir, "schema.sql")
	if err := os.WriteFile(schemaPath, []byte(schema), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	script := strings.NewReplacer(
		"/schema/schema.sql", schemaPath,
		"{{ .Values.clickhouse.database }}", database,
		"{{ .Values.clickhouse.tables.traces }}", "otel_traces",
		"{{ .Values.clickhouse.tables.logs }}", "otel_logs",
		"{{ .Values.clickhouse.tables.metricsGauge }}", "otel_metrics_gauge",
		"{{ .Values.clickhouse.tables.metricsSum }}", "otel_metrics_sum",
		"{{ .Values.clickhouse.tables.metricsHistogram }}", "otel_metrics_histogram",
		"{{ .Values.clickhouse.tables.metricsExponentialHistogram }}", "otel_metrics_exponential_histogram",
		"{{ .Values.clickhouse.tables.metricsSummary }}", "otel_metrics_summary",
		"{{ .Values.clickhouse.dedup.enabled }}", fmt.Sprintf("%t", dedup),
	).Replace(string(scriptRaw))
	if strings.Contains(script, "{{") {
		t.Fatalf("rendered migrate.sh still contains a placeholder")
	}
	scriptPath := filepath.Join(dir, "migrate.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ch.exec(t, "CREATE DATABASE `"+database+"`")
	t.Cleanup(func() {
		ch.exec(t, "DROP DATABASE IF EXISTS `"+database+"` SYNC")
	})
	applyStatements(t, ch, oldSchema(t, renderSchema(t, string(schemaRaw), database, fixtureDedup)))
	return &harness{t: t, ch: ch, database: database, script: scriptPath, shimDir: shimDir}
}

func (h *harness) run() (string, error) {
	cmd := exec.Command("sh", h.script)
	cmd.Env = append(os.Environ(),
		"PATH="+h.shimDir+":"+os.Getenv("PATH"),
		"CLICKHOUSE_HOST=127.0.0.1",
		"CLICKHOUSE_ADMIN_USER=default",
		"CLICKHOUSE_ADMIN_PASSWORD=",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *harness) tableKey(table string) string {
	h.t.Helper()
	return h.ch.exec(h.t, "SELECT sorting_key FROM system.tables WHERE database = '"+h.database+"' AND name = '"+table+"'")
}

func (h *harness) tableEngine(table string) string {
	h.t.Helper()
	return h.ch.exec(h.t, "SELECT engine FROM system.tables WHERE database = '"+h.database+"' AND name = '"+table+"'")
}

func (h *harness) count(table string) string {
	h.t.Helper()
	return h.ch.exec(h.t, "SELECT count() FROM `"+h.database+"`.`"+table+"`")
}

func TestMigrationReKeysMetricsAndPreservesRows(t *testing.T) {
	h := newHarness(t, true)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_sum` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','m','2026-11-16 00:37:12','2026-11-16 00:37:12',1.5,{'k':'v'})")
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_sum` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','m','2026-11-16 00:37:12','2026-11-16 00:37:12',1.5,{'k':'v'})")
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_sum` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','m','2026-11-16 00:37:12','2026-11-16 00:37:12',2.5,{'k':'v'})")
	// ReplacingMergeTree deduplicates replay records; compare the logical
	// result before and after rather than the physical row count.
	before := h.ch.exec(t, "SELECT count(), sum(Value) FROM `"+h.database+"`.`otel_metrics_sum` FINAL")

	if out, err := h.run(); err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	for _, table := range []string{"otel_metrics_gauge", "otel_metrics_sum", "otel_metrics_histogram", "otel_metrics_exponential_histogram", "otel_metrics_summary"} {
		if key := h.tableKey(table); !strings.HasPrefix(key, "toStartOfFiveMinutes(TimeUnix), MetricName") {
			t.Fatalf("%s sorting_key = %q, want the five-minute bucket key", table, key)
		}
	}
	if got := h.ch.exec(t, "SELECT count(), sum(Value) FROM `"+h.database+"`.`otel_metrics_sum` FINAL"); got != before {
		t.Fatalf("logical rows after migration = %s, want %s", got, before)
	}
	if got := h.ch.exec(t, "SELECT count() FROM `"+h.database+"`.`otel_metrics_sum` WHERE Value = 2.5"); got != "1" {
		t.Fatalf("distinct samples after migration = %s, want 1", got)
	}
}

func TestMigrationIsIdempotent(t *testing.T) {
	h := newHarness(t, true)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_gauge` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','g','2026-11-16 00:40:00','2026-11-16 00:40:00',1,{})")
	if out, err := h.run(); err != nil {
		t.Fatalf("first migrate failed: %v\n%s", err, out)
	}
	if out, err := h.run(); err != nil {
		t.Fatalf("second migrate failed: %v\n%s", err, out)
	}
	if got := h.count("otel_metrics_gauge"); got != "1" {
		t.Fatalf("rows after re-run = %s, want 1", got)
	}
	if key := h.tableKey("otel_metrics_gauge"); !strings.HasPrefix(key, "toStartOfFiveMinutes(TimeUnix)") {
		t.Fatalf("sorting_key after re-run = %q", key)
	}
}

func TestMigrationRecoversInterruptedRename(t *testing.T) {
	h := newHarness(t, true)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_gauge` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','g','2026-11-16 00:40:00','2026-11-16 00:40:00',1,{})")
	h.ch.exec(t, "RENAME TABLE `"+h.database+"`.`otel_metrics_gauge` TO `"+h.database+"`.`otel_metrics_gauge__skopos_pre_dedup`")
	if out, err := h.run(); err != nil {
		t.Fatalf("recovery migrate failed: %v\n%s", err, out)
	}
	if got := h.count("otel_metrics_gauge"); got != "1" {
		t.Fatalf("rows after recovery = %s, want 1", got)
	}
	if key := h.tableKey("otel_metrics_gauge"); !strings.HasPrefix(key, "toStartOfFiveMinutes(TimeUnix)") {
		t.Fatalf("sorting_key after recovery = %q, want the bucket key", key)
	}
}

func TestMigrationKeepsIngestDuringCopy(t *testing.T) {
	h := newHarness(t, true)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_sum` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','before','2026-11-16 00:40:00','2026-11-16 00:40:00',1,{})")
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mu sync.Mutex
	inserted := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		n := 0
		for {
			select {
			case <-stop:
				return
			default:
				n++
				// Vary the value so each row is distinct; identical replay
				// rows would collapse on the ReplacingMergeTree key.
				_, err := h.ch.try(fmt.Sprintf("INSERT INTO `%s`.`otel_metrics_sum` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','during-copy','2026-11-16 00:40:00','2026-11-16 00:40:00',%d,{})", h.database, n))
				if err == nil {
					mu.Lock()
					inserted++
					mu.Unlock()
				}
			}
		}
	}()
	out, err := h.run()
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	mu.Lock()
	want := inserted + 1
	mu.Unlock()
	if got := h.count("otel_metrics_sum"); got != fmt.Sprintf("%d", want) {
		t.Fatalf("rows after concurrent ingest = %s, want %d (1 before + %d during)", got, want, inserted)
	}
}

func TestMigrationConvertsAndReKeysPreDedupTables(t *testing.T) {
	h := newHarnessProfile(t, true, false)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_gauge` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','g','2026-11-16 00:40:00','2026-11-16 00:40:00',1,{})")
	if out, err := h.run(); err != nil {
		t.Fatalf("pre-dedup migrate failed: %v\n%s", err, out)
	}
	if engine := h.tableEngine("otel_metrics_gauge"); engine != "ReplacingMergeTree" {
		t.Fatalf("engine after migration = %q, want ReplacingMergeTree", engine)
	}
	if key := h.tableKey("otel_metrics_gauge"); !strings.HasPrefix(key, "toStartOfFiveMinutes(TimeUnix)") {
		t.Fatalf("sorting_key after migration = %q, want the bucket key", key)
	}
	if got := h.count("otel_metrics_gauge"); got != "1" {
		t.Fatalf("rows after migration = %s, want 1", got)
	}
}

func TestReplayDedupParityAcrossMetricsTables(t *testing.T) {
	h := newHarness(t, true)
	if out, err := h.run(); err != nil {
		t.Fatalf("migrate failed: %v\n%s", err, out)
	}
	cases := []struct {
		table   string
		columns string
		first   string
		second  string
	}{
		{"otel_metrics_gauge", "Value", "1", "2"},
		{"otel_metrics_sum", "Value", "1", "2"},
		{"otel_metrics_histogram", "Count, Sum", "1, 1.5", "2, 2.5"},
		{"otel_metrics_exponential_histogram", "Count, Sum, PositiveBucketCounts", "1, 1.5, [1]", "2, 2.5, [2]"},
		{"otel_metrics_summary", "Count, Sum", "1, 1.5", "2, 2.5"},
	}
	for _, tc := range cases {
		t.Run(tc.table, func(t *testing.T) {
			insert := func(values, attrs string) {
				t.Helper()
				h.ch.exec(t, "INSERT INTO `"+h.database+"`.`"+tc.table+"` (ServiceName, MetricName, TimeUnix, StartTimeUnix, "+tc.columns+", Attributes) VALUES ('svc','"+tc.table+"','2026-11-16 00:42:00','2026-11-16 00:42:00',"+values+","+attrs+")")
			}
			// The same replay with a different attribute insertion order must
			// collapse: the key and the row hash both mapSort the map.
			insert(tc.first, "{'a':'1','b':'2'}")
			insert(tc.first, "{'b':'2','a':'1'}")
			// A distinct sample differs in a non-key column and must survive.
			insert(tc.second, "{'a':'1','b':'2'}")

			if got := h.ch.exec(t, "SELECT count() FROM `"+h.database+"`.`"+tc.table+"` FINAL"); got != "2" {
				t.Fatalf("FINAL rows = %s, want 2 (one replay collapsed, distinct sample kept)", got)
			}
		})
	}
}

func TestFastProfileMigrationReKeys(t *testing.T) {
	h := newHarness(t, false)
	h.ch.exec(t, "INSERT INTO `"+h.database+"`.`otel_metrics_gauge` (ServiceName, MetricName, TimeUnix, StartTimeUnix, Value, Attributes) VALUES ('svc','g','2026-11-16 00:40:00','2026-11-16 00:40:00',1,{})")
	if out, err := h.run(); err != nil {
		t.Fatalf("fast profile migrate failed: %v\n%s", err, out)
	}
	if key := h.tableKey("otel_metrics_gauge"); !strings.HasPrefix(key, "toStartOfFiveMinutes(TimeUnix)") {
		t.Fatalf("fast profile sorting_key = %q, want the bucket key", key)
	}
	if got := h.count("otel_metrics_gauge"); got != "1" {
		t.Fatalf("fast profile rows = %s, want 1", got)
	}
}
