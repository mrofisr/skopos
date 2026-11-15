package scale

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Writer sends JSONEachRow batches to the ClickHouse HTTP endpoint.
type Writer struct {
	URL      string
	User     string
	Password string
	Database string
	Batch    int
	Client   *http.Client
}

const (
	scopeName    = "skopos/scale"
	scopeVersion = "1"
	nanoTime     = "2006-01-02 15:04:05.000000000"

	metricSum       = "http_requests_total"
	metricHistogram = "http_request_duration"
	servicePrefix   = "service-"
)

var histogramBounds = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Write inserts the dataset into ClickHouse. The target tables must already
// exist. A re-run is safe on the ReplacingMergeTree schema because the rows are
// identical and collapse during a merge or FINAL.
func (w *Writer) Write(ctx context.Context, d Dataset, now time.Time) error {
	if w.Batch <= 0 {
		return fmt.Errorf("scale: batch must be positive")
	}
	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	if err := w.writeTable(ctx, client, "otel_metrics_sum", func(yield func([]byte) error) error {
		return writeSumRows(d, now, yield)
	}); err != nil {
		return err
	}
	if err := w.writeTable(ctx, client, "otel_metrics_histogram", func(yield func([]byte) error) error {
		return writeHistogramRows(d, now, yield)
	}); err != nil {
		return err
	}
	if err := w.writeTable(ctx, client, "otel_logs", func(yield func([]byte) error) error {
		return writeLogRows(d, now, yield)
	}); err != nil {
		return err
	}
	return w.writeTable(ctx, client, "otel_traces", func(yield func([]byte) error) error {
		return writeTraceRows(d, now, yield)
	})
}

func (w *Writer) writeTable(ctx context.Context, client *http.Client, table string, emit func(func([]byte) error) error) error {
	var buf bytes.Buffer
	rows := 0
	flush := func() error {
		if rows == 0 {
			return nil
		}
		body := append([]byte(nil), buf.Bytes()...)
		buf.Reset()
		rows = 0
		return w.insert(ctx, client, table, body)
	}
	if err := emit(func(row []byte) error {
		buf.Write(row)
		buf.WriteByte('\n')
		rows++
		if rows >= w.Batch {
			return flush()
		}
		return nil
	}); err != nil {
		return err
	}
	return flush()
}

func (w *Writer) insert(ctx context.Context, client *http.Client, table string, body []byte) error {
	query := "INSERT INTO `" + w.Database + "`.`" + table + "` FORMAT JSONEachRow"
	params := url.Values{}
	params.Set("database", w.Database)
	params.Set("query", query)
	endpoint := strings.TrimRight(w.URL, "/") + "/?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("scale: build insert %s: %w", table, err)
	}
	if w.User != "" {
		req.SetBasicAuth(w.User, w.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("scale: insert %s: %w", table, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("scale: insert %s: HTTP %d: %s", table, resp.StatusCode, strings.TrimSpace(string(message)))
	}
	return nil
}

func writeSumRows(d Dataset, now time.Time, yield func([]byte) error) error {
	profile := d.Profile
	start := now.Add(-profile.Range)
	for series := 0; series < profile.Series; series++ {
		service := serviceName(series % profile.Services)
		instance := fmt.Sprintf("instance-%d", series/profile.Services)
		method := "GET"
		if (series/profile.Services)%2 == 1 {
			method = "POST"
		}
		status := "200"
		if (series/profile.Services)%5 == 0 {
			status = "500"
		}
		for sample := 0; sample < profile.Samples; sample++ {
			at := start.Add(time.Duration(sample) * profile.Step)
			row := map[string]any{
				"ResourceAttributes":     map[string]string{"service.name": service},
				"ScopeName":              scopeName,
				"ScopeVersion":           scopeVersion,
				"ServiceName":            service,
				"MetricName":             metricSum,
				"MetricDescription":      "synthetic scale counter",
				"MetricUnit":             "1",
				"Attributes":             map[string]string{"instance": instance, "http.method": method, "http.response.status_code": status},
				"StartTimeUnix":          metricTime(start),
				"TimeUnix":               metricTime(at),
				"Value":                  float64(sample + 1 + series%7),
				"Flags":                  0,
				"AggregationTemporality": 2,
				"IsMonotonic":            1,
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return fmt.Errorf("scale: encode sum row: %w", err)
			}
			if err := yield(encoded); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeHistogramRows(d Dataset, now time.Time, yield func([]byte) error) error {
	profile := d.Profile
	start := now.Add(-profile.Range)
	for service := 0; service < profile.Services; service++ {
		name := serviceName(service)
		for sample := 0; sample < profile.Samples; sample++ {
			at := start.Add(time.Duration(sample) * profile.Step)
			count := uint64(sample + 1)
			buckets := make([]uint64, len(histogramBounds)+1)
			buckets[len(buckets)/2] = count
			row := map[string]any{
				"ResourceAttributes":     map[string]string{"service.name": name},
				"ScopeName":              scopeName,
				"ScopeVersion":           scopeVersion,
				"ServiceName":            name,
				"MetricName":             metricHistogram,
				"MetricDescription":      "synthetic scale histogram",
				"MetricUnit":             "s",
				"Attributes":             map[string]string{"instance": name, "http.method": "GET", "http.response.status_code": "200"},
				"StartTimeUnix":          metricTime(start),
				"TimeUnix":               metricTime(at),
				"Count":                  count,
				"Sum":                    float64(count) * 0.1,
				"BucketCounts":           buckets,
				"ExplicitBounds":         histogramBounds,
				"Flags":                  0,
				"Min":                    0.05,
				"Max":                    0.1,
				"AggregationTemporality": 2,
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				return fmt.Errorf("scale: encode histogram row: %w", err)
			}
			if err := yield(encoded); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeLogRows(d Dataset, now time.Time, yield func([]byte) error) error {
	profile := d.Profile
	if profile.Logs == 0 {
		return nil
	}
	start := now.Add(-profile.Range)
	step := profile.Range / time.Duration(profile.Logs)
	if step <= 0 {
		step = time.Nanosecond
	}
	for i := 0; i < profile.Logs; i++ {
		service := serviceName(i % profile.Services)
		at := start.Add(time.Duration(i) * step)
		level, status, severity := "info", 200, 9
		if i%2 == 1 {
			level, status, severity = "error", 500, 17
		}
		message := "synthetic scale log"
		if level == "error" {
			message = "synthetic scale log failed"
		}
		row := map[string]any{
			"Timestamp":          at.UTC().Format(nanoTime),
			"TraceId":            "",
			"SpanId":             "",
			"TraceFlags":         0,
			"SeverityText":       level,
			"SeverityNumber":     severity,
			"ServiceName":        service,
			"Body":               fmt.Sprintf(`{"level":%q,"status":%d,"msg":%q}`, level, status, message),
			"ResourceSchemaUrl":  "",
			"ResourceAttributes": map[string]string{"service.name": service},
			"ScopeSchemaUrl":     "",
			"ScopeName":          scopeName,
			"ScopeVersion":       scopeVersion,
			"ScopeAttributes":    map[string]string{},
			"LogAttributes":      map[string]string{"level": level},
			"EventName":          "",
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("scale: encode log row: %w", err)
		}
		if err := yield(encoded); err != nil {
			return err
		}
	}
	return nil
}

func writeTraceRows(d Dataset, now time.Time, yield func([]byte) error) error {
	profile := d.Profile
	if profile.Spans == 0 {
		return nil
	}
	const spansPerTrace = 10
	start := now.Add(-profile.Range)
	step := profile.Range / time.Duration(profile.Spans)
	if step <= 0 {
		step = time.Nanosecond
	}
	for i := 0; i < profile.Spans; i++ {
		service := serviceName(i % profile.Services)
		group := uint64(i / spansPerTrace)
		parent := ""
		if i%spansPerTrace != 0 {
			parent = spanID(d.Seed, group*spansPerTrace)
		}
		at := start.Add(time.Duration(i) * step)
		row := map[string]any{
			"Timestamp":          at.UTC().Format(nanoTime),
			"TraceId":            traceID(d.Seed, group),
			"SpanId":             spanID(d.Seed, uint64(i)),
			"ParentSpanId":       parent,
			"TraceState":         "",
			"SpanName":           "GET /",
			"SpanKind":           "2",
			"ServiceName":        service,
			"ResourceAttributes": map[string]string{"service.name": service},
			"ScopeName":          scopeName,
			"ScopeVersion":       scopeVersion,
			"SpanAttributes":     map[string]string{"http.method": "GET", "http.route": "/"},
			"Duration":           uint64(1_000_000 + (i%100)*100_000),
			"StatusCode":         "0",
			"StatusMessage":      "",
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("scale: encode trace row: %w", err)
		}
		if err := yield(encoded); err != nil {
			return err
		}
	}
	return nil
}

func serviceName(index int) string {
	return fmt.Sprintf("%s%d", servicePrefix, index)
}

func metricTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05")
}

func traceID(seed int64, group uint64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], mix64(uint64(seed), group, 0))
	binary.BigEndian.PutUint64(b[8:], mix64(uint64(seed), group, 2))
	return hex.EncodeToString(b[:])
}

func spanID(seed int64, index uint64) string {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], mix64(uint64(seed), index, 1))
	return hex.EncodeToString(b[:])
}
