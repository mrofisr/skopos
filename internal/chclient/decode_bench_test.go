package chclient

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func benchmarkResponse(rows int) []byte {
	var b bytes.Buffer
	b.Grow(rows * 180)
	b.WriteString(`{"meta":[{"name":"MetricName","type":"String"},{"name":"TimeUnix","type":"DateTime64(9)"},{"name":"ServiceName","type":"String"},{"name":"Attributes","type":"Map(String, String)"},{"name":"Value","type":"Float64"}],"data":[`)
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b,
			`{"MetricName":"http_requests_total","TimeUnix":"2026-11-15 12:00:00.000000000","ServiceName":"service-%d","Attributes":{"service.name":"service-%d","host.name":"host-%d"},"Value":%d.5}`,
			i%100, i%100, i%10, i)
	}
	b.WriteString(`],"rows":`)
	b.WriteString(strconv.Itoa(rows))
	b.WriteString(`}`)
	return b.Bytes()
}

func BenchmarkDecodeResult(b *testing.B) {
	data := benchmarkResponse(50_000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decodeResult(data, http.StatusOK); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkCompactResponse(rows int) []byte {
	var b bytes.Buffer
	b.Grow(rows * 150)
	b.WriteString(`{"meta":[{"name":"MetricName","type":"String"},{"name":"TimeUnix","type":"DateTime64(9)"},{"name":"ServiceName","type":"String"},{"name":"Attributes","type":"Map(String, String)"},{"name":"Value","type":"Float64"}],"data":[`)
	for i := 0; i < rows; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b,
			`["http_requests_total","2026-11-15 12:00:00.000000000","service-%d",{"service.name":"service-%d","host.name":"host-%d"},%d.5]`,
			i%100, i%100, i%10, i)
	}
	b.WriteString(`],"rows":`)
	b.WriteString(strconv.Itoa(rows))
	b.WriteString(`}`)
	return b.Bytes()
}

func BenchmarkDecodeCompactResult(b *testing.B) {
	data := benchmarkCompactResponse(50_000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decodeCompactResult(data, http.StatusOK); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkGroupedResponse(series int) []byte {
	var b bytes.Buffer
	b.Grow(series * 150)
	b.WriteString(`{"meta":[{"name":"MetricName","type":"String"},{"name":"ServiceName","type":"String"},{"name":"Attributes","type":"Map(String, String)"},{"name":"Times","type":"Array(DateTime64(9))"},{"name":"Values","type":"Array(Float64)"}],"data":[`)
	for i := 0; i < series; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b,
			`["http_requests_total","service-%d",{"service.name":"service-%d","host.name":"host-%d"},["2026-11-15 12:00:00.000000000","2026-11-15 12:00:15.000000000","2026-11-15 12:00:30.000000000","2026-11-15 12:00:45.000000000","2026-11-15 12:01:00.000000000"],[%d.5,%d.5,%d.5,%d.5,%d.5]]`,
			i%100, i%100, i%10, i, i+1, i+2, i+3, i+4)
	}
	b.WriteString(`],"rows":`)
	b.WriteString(strconv.Itoa(series))
	b.WriteString(`}`)
	return b.Bytes()
}

func BenchmarkDecodeGroupedResult(b *testing.B) {
	data := benchmarkGroupedResponse(10_000)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := decodeCompactResult(data, http.StatusOK); err != nil {
			b.Fatal(err)
		}
	}
}
