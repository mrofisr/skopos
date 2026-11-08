#!/usr/bin/env python3
"""Run the pinned upstream Loki benchmark corpus against Loki and Skopos.

The corpus is generated from the Loki 3.5.7 benchmark package by
test/compat/loki/upstream.sh. The upstream generator only defines queries and
time ranges, not log data, so this harness seeds one synthetic stream per
selector with bodies that exercise the corpus filters, then compares the
normalized responses.

The corpus timestamps are fixed at 2024-01-01. Every case is shifted by the same
offset so the corpus ends a few minutes before the run starts; this keeps the
fixture inside Loki's retention and the ClickHouse TTL without changing the
query text or the relative time ranges.

Usage:
  test/compat/loki/upstream.sh run --gateway-url http://127.0.0.1:28082
  python3 test/compat/loki/upstream.py --corpus ... --report ...
"""

from __future__ import annotations

import argparse
import datetime as dt
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

DROP_LABELS = {"__error__", "__error_details__", "detected_level_extracted"}
DEFAULT_SERVICE_NAME = "upstream"
DEFAULT_LABELS = {
    "cluster": "cluster-1",
    "namespace": "namespace-1",
    "pod": "pod-1",
    "container": "container-1",
    "datacenter": "dc1",
    "component": "api",
    "env": "prod",
    "region": "us-east-1",
}
# Bodies cover the line filters and parser stages used by the upstream corpus.
VARIANTS = [
    ("error", '{"level":"error","duration_seconds":0.5,"msg":"upstream error"}'),
    ("error", 'level=error component=api duration_seconds=0.5 msg="upstream logfmt error"'),
    ("warn", '{"level":"warn","duration_seconds":0.2,"msg":"upstream warn"}'),
    ("warn", "upstream exception: connection reset"),
    ("info", '{"level":"info","duration_seconds":0.05,"msg":"upstream ok"}'),
    ("debug", "plain upstream line"),
]
SELECTOR_RE = re.compile(r"\{([^}]*)\}")
MATCHER_RE = re.compile(r'\s*([A-Za-z_][A-Za-z0-9_]*)\s*=\s*"([^"]*)"\s*(?:,|$)')


def http_json(url, data=None, method=None, timeout=60):
    headers = {}
    body = None
    if data is not None:
        body = json.dumps(data).encode()
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(url, data=body, headers=headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            payload = response.read()
            return response.status, json.loads(payload) if payload else None
    except urllib.error.HTTPError as error:
        payload = error.read()
        try:
            parsed = json.loads(payload) if payload else None
        except json.JSONDecodeError:
            parsed = {"raw": payload.decode(errors="replace")}
        return error.code, parsed


def wait_ready(base, timeout=120):
    deadline = time.time() + timeout
    url = base.rstrip("/") + "/ready"
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=2) as response:
                if response.status == 200:
                    return
        except Exception:
            pass
        time.sleep(1)
    raise SystemExit(f"Loki did not become ready at {url}")


def parse_ts(value):
    return dt.datetime.fromisoformat(value.replace("Z", "+00:00"))


def to_nanos(value):
    return int(parse_ts(value).timestamp() * 1_000_000_000)


def ns_to_rfc3339(value):
    seconds, nanos = divmod(int(value), 1_000_000_000)
    stamp = dt.datetime.fromtimestamp(seconds, tz=dt.timezone.utc)
    return stamp.strftime("%Y-%m-%dT%H:%M:%S") + f".{nanos:09d}Z"


def clickhouse_timestamp(nanos):
    seconds, fraction = divmod(int(nanos), 1_000_000_000)
    stamp = dt.datetime.fromtimestamp(seconds, tz=dt.timezone.utc)
    return stamp.strftime("%Y-%m-%d %H:%M:%S") + f".{fraction:09d}"


def selector_labels(query):
    match = SELECTOR_RE.search(query)
    if not match:
        raise ValueError(f"no stream selector in {query!r}")
    labels = {}
    rest = match.group(1)
    while rest.strip():
        matcher = MATCHER_RE.match(rest)
        if not matcher:
            raise ValueError(f"unsupported selector {match.group(1)!r}")
        labels[matcher.group(1)] = matcher.group(2)
        rest = rest[matcher.end():]
    return labels


def build_seed_entries(cases, shift):
    """Create one stream per selector and body variant at every case end."""
    seen = set()
    entries = []
    for case in cases:
        end = to_nanos(case["End"]) + shift
        labels = selector_labels(case["Query"])
        for name, value in DEFAULT_LABELS.items():
            labels.setdefault(name, value)
        labels.setdefault("service_name", DEFAULT_SERVICE_NAME)
        for index, (level, body) in enumerate(VARIANTS):
            timestamp = end - (index + 1) * 10_000_000_000
            # detected_level is structured metadata in Loki, not an index
            # label. Loki copies an explicitly pushed detected_level label into
            # detected_level_extracted and returns duplicate streams.
            key = (tuple(sorted(labels.items())), level, timestamp, body)
            if key in seen:
                continue
            seen.add(key)
            entries.append({
                "timestamp": timestamp,
                "body": body,
                "labels": dict(labels),
                "detected_level": level,
            })
    entries.sort(key=lambda item: (item["timestamp"], item["body"]))
    return entries


def push_loki(base, entries):
    streams = {}
    for entry in entries:
        key = tuple(sorted(entry["labels"].items()))
        streams.setdefault(key, {"stream": entry["labels"], "values": []})
        metadata = {"detected_level": entry["detected_level"]}
        streams[key]["values"].append([str(entry["timestamp"]), entry["body"], metadata])
    status, payload = http_json(base.rstrip("/") + "/loki/api/v1/push", {"streams": list(streams.values())})
    if status not in (200, 204):
        raise SystemExit(f"Loki push failed: {status} {payload}")


def seed_clickhouse(url, database, table, entries, truncate):
    rows = []
    for entry in entries:
        labels = dict(entry["labels"])
        labels["detected_level"] = entry["detected_level"]
        resource = {name: value for name, value in labels.items() if name != "service_name"}
        rows.append(json.dumps({
            "Timestamp": clickhouse_timestamp(entry["timestamp"]),
            "Body": entry["body"],
            "ServiceName": labels.get("service_name", ""),
            "SeverityText": "",
            "SeverityNumber": 0,
            "TraceId": "",
            "SpanId": "",
            "EventName": "",
            "LogAttributes": {},
            "ResourceAttributes": resource,
        }))
    if truncate:
        truncate_sql = "TRUNCATE TABLE `" + database + "`.`" + table + "`"
        request = urllib.request.Request(url, data=truncate_sql.encode(), method="POST")
        urllib.request.urlopen(request, timeout=30).read()
    body = "INSERT INTO `" + database + "`.`" + table + "` FORMAT JSONEachRow\n" + "\n".join(rows)
    request = urllib.request.Request(url, data=body.encode(), method="POST")
    urllib.request.urlopen(request, timeout=60).read()


def request_params(case):
    params = {
        "query": case["Query"],
        "start": ns_to_rfc3339(to_nanos(case["Start"]) + case["_shift"]),
        "end": ns_to_rfc3339(to_nanos(case["End"]) + case["_shift"]),
        "direction": "backward" if case.get("Direction") == 1 else "forward",
        "limit": "5000",
    }
    if case.get("Step"):
        # Loki rejects nanosecond-duration strings for very large steps; float
        # seconds are accepted by both Loki and Skopos.
        params["step"] = f"{case['Step'] / 1_000_000_000:.9f}"
    return params


def run_case(base, case):
    url = base.rstrip("/") + "/loki/api/v1/query_range?" + urllib.parse.urlencode(request_params(case))
    return http_json(url)


def normalized_labels(labels):
    # Loki suffixes an extracted label that collides with an index label, for
    # example component_extracted. The plain component label is still compared.
    return {
        name: value
        for name, value in (labels or {}).items()
        if name not in DROP_LABELS and not name.endswith("_extracted")
    }


def normalize_log(payload):
    lines = []
    for stream in (payload or {}).get("data", {}).get("result", []):
        labels = normalized_labels(stream.get("stream", {}))
        for value in stream.get("values", []):
            lines.append({
                "timestamp": int(value[0]),
                "body": value[1],
                "labels": labels,
            })
    lines.sort(key=lambda line: (line["timestamp"], line["body"], json.dumps(line["labels"], sort_keys=True)))
    return lines


def metric_time(value):
    """Compare evaluation timestamps at Loki's millisecond precision."""
    return int(round(float(value) * 1000))


def normalize_metric(payload):
    series = []
    for item in (payload or {}).get("data", {}).get("result", []):
        metric = normalized_labels(item.get("metric", {}))
        if "values" in item:
            pairs = [(metric_time(pair[0]), float(pair[1])) for pair in item["values"]]
        else:
            pairs = [(metric_time(item["value"][0]), float(item["value"][1]))]
        pairs.sort(key=lambda pair: pair[0])
        series.append({
            "metric": metric,
            "values": pairs,
        })
    series.sort(key=lambda item: json.dumps(item["metric"], sort_keys=True))
    return series


def compare_log(want, got):
    if len(want) != len(got):
        return f"line count {len(got)}, want {len(want)}"
    for index, (expected, actual) in enumerate(zip(want, got)):
        if expected["timestamp"] != actual["timestamp"]:
            return (
                f"line {index} timestamp {ns_to_rfc3339(actual['timestamp'])}, "
                f"want {ns_to_rfc3339(expected['timestamp'])}"
            )
        if expected["body"] != actual["body"]:
            return f"line {index} body {actual['body']!r}, want {expected['body']!r}"
        if expected["labels"] != actual["labels"]:
            return (
                f"line {index} labels {json.dumps(actual['labels'], sort_keys=True)}, "
                f"want {json.dumps(expected['labels'], sort_keys=True)}"
            )
    return None


def compare_metric(want, got):
    if len(want) != len(got):
        return f"series count {len(got)}, want {len(want)}"
    for index, (expected, actual) in enumerate(zip(want, got)):
        if expected["metric"] != actual["metric"]:
            return (
                f"series {index} labels {json.dumps(actual['metric'], sort_keys=True)}, "
                f"want {json.dumps(expected['metric'], sort_keys=True)}"
            )
        if len(expected["values"]) != len(actual["values"]):
            return (
                f"series {index} point count {len(actual['values'])}, "
                f"want {len(expected['values'])}"
            )
        for point, (expected_point, actual_point) in enumerate(zip(expected["values"], actual["values"])):
            if expected_point[0] != actual_point[0]:
                return (
                    f"series {index} point {point} timestamp {actual_point[0]}, "
                    f"want {expected_point[0]}"
                )
            tolerance = max(1e-9, abs(expected_point[1]) * 1e-9)
            if abs(expected_point[1] - actual_point[1]) > tolerance:
                return (
                    f"series {index} point {point} value {actual_point[1]}, "
                    f"want {expected_point[1]}"
                )
    return None


def error_message(payload):
    if not payload:
        return "empty response"
    if isinstance(payload, dict):
        return str(payload.get("error") or payload.get("message") or payload)[:300]
    return str(payload)[:300]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--corpus", default=os.path.join(os.path.dirname(os.path.abspath(__file__)), "upstream", "corpus.json"))
    parser.add_argument("--loki-url", default=os.environ.get("LOKI_URL", "http://127.0.0.1:13100"))
    parser.add_argument("--gateway-url", default=os.environ.get("GATEWAY_URL", ""))
    parser.add_argument("--clickhouse-url", default=os.environ.get("CLICKHOUSE_URL", "http://127.0.0.1:18124"))
    parser.add_argument("--database", default=os.environ.get("CLICKHOUSE_DATABASE", "otel"))
    parser.add_argument("--table", default=os.environ.get("LOGS_TABLE", "otel_logs"))
    parser.add_argument("--truncate", action="store_true", help="truncate the ClickHouse logs table before seeding")
    parser.add_argument("--report", help="write the Markdown diff report to this path")
    parser.add_argument("--fail-on-mismatch", action="store_true")
    args = parser.parse_args()

    with open(args.corpus, encoding="utf-8") as handle:
        corpus = json.load(handle)
    provenance = corpus.get("provenance", {})
    cases = corpus["cases"]
    if not cases:
        raise SystemExit("corpus is empty")

    now = dt.datetime.now(dt.timezone.utc)
    corpus_end = max(parse_ts(case["End"]) for case in cases)
    target_end = now - dt.timedelta(minutes=5)
    shift = int((target_end - corpus_end).total_seconds() * 1_000_000_000)
    for case in cases:
        case["_shift"] = shift

    entries = build_seed_entries(cases, shift)
    wait_ready(args.loki_url)
    push_loki(args.loki_url, entries)
    time.sleep(2)

    gateway_entries = 0
    if args.gateway_url:
        seed_clickhouse(args.clickhouse_url, args.database, args.table, entries, args.truncate)
        gateway_entries = len(entries)

    results = []
    for case in cases:
        name = case["Query"] + (" [backward]" if case.get("Direction") == 1 else "")
        try:
            loki_status, loki_payload = run_case(args.loki_url, case)
        except Exception as error:  # noqa: BLE001 - report every harness error
            results.append({"name": name, "query": case["Query"], "kind": "harness-error", "detail": str(error)})
            continue
        if loki_status != 200:
            results.append({
                "name": name,
                "query": case["Query"],
                "kind": "reference-error",
                "detail": f"Loki {loki_status}: {error_message(loki_payload)}",
            })
            continue

        kind = "metric" if case.get("Step") else "log"
        loki_normalized = normalize_metric(loki_payload) if kind == "metric" else normalize_log(loki_payload)

        if not args.gateway_url:
            results.append({"name": name, "query": case["Query"], "kind": "match", "detail": f"{len(loki_normalized)} results"})
            continue

        try:
            gateway_status, gateway_payload = run_case(args.gateway_url, case)
        except Exception as error:  # noqa: BLE001 - report every harness error
            results.append({"name": name, "query": case["Query"], "kind": "harness-error", "detail": str(error)})
            continue
        if gateway_status == 400:
            results.append({
                "name": name,
                "query": case["Query"],
                "kind": "unsupported",
                "detail": error_message(gateway_payload),
            })
            continue
        if gateway_status != 200:
            results.append({
                "name": name,
                "query": case["Query"],
                "kind": "gateway-error",
                "detail": f"{gateway_status}: {error_message(gateway_payload)}",
            })
            continue

        gateway_normalized = normalize_metric(gateway_payload) if kind == "metric" else normalize_log(gateway_payload)
        problem = compare_metric(loki_normalized, gateway_normalized) if kind == "metric" else compare_log(loki_normalized, gateway_normalized)
        if problem:
            results.append({"name": name, "query": case["Query"], "kind": "mismatch", "detail": problem})
        else:
            results.append({"name": name, "query": case["Query"], "kind": "match", "detail": f"{len(loki_normalized)} results"})

    counts = {}
    for result in results:
        counts[result["kind"]] = counts.get(result["kind"], 0) + 1
    bad = counts.get("harness-error", 0) + counts.get("reference-error", 0) + counts.get("gateway-error", 0)

    summary = ", ".join(f"{name}={count}" for name, count in sorted(counts.items()))
    print(f"cases: {len(results)}; {summary}")
    for result in results:
        if result["kind"] not in ("match",):
            print(f"{result['kind'].upper():14s} {result['name']}\n  {result['detail']}", file=sys.stderr)

    if args.report:
        write_report(args.report, provenance, args, len(cases), len(entries), gateway_entries, results, counts)

    if bad:
        raise SystemExit(f"{bad} harness/reference errors")
    if args.fail_on_mismatch and (counts.get("mismatch") or counts.get("unsupported")):
        raise SystemExit("mismatches or unsupported queries found")


def write_report(path, provenance, args, case_count, loki_entries, gateway_entries, results, counts):
    lines = []
    lines.append("# LogQL upstream baseline")
    lines.append("")
    lines.append("Generated by `test/compat/loki/upstream-run.sh`. Do not edit by hand.")
    lines.append("")
    lines.append("## Provenance")
    lines.append("")
    lines.append(f"- Upstream: `{provenance.get('project', 'github.com/grafana/loki')}`")
    lines.append(f"- Version: `{provenance.get('version', '3.5.7')}`")
    lines.append(f"- Commit: `{provenance.get('commit', '')}`")
    lines.append(f"- Archive SHA-256: `{provenance.get('archive_sha256', '')}`")
    lines.append(f"- Generator: `{provenance.get('generator', '')}` with seed `{provenance.get('seed', '1')}`")
    lines.append(f"- Run date: {dt.datetime.now(dt.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')}")
    lines.append(f"- Corpus cases: {case_count}")
    lines.append(f"- Synthetic log entries seeded into Loki: {loki_entries}")
    lines.append(f"- Synthetic log entries seeded into ClickHouse: {gateway_entries}")
    lines.append(f"- Reference: `{args.loki_url}`")
    lines.append(f"- Gateway: `{args.gateway_url or 'not configured'}`")
    if args.gateway_url:
        lines.append(f"- ClickHouse: `{args.clickhouse_url}` database `{args.database}` table `{args.table}`")
    lines.append("")
    lines.append("The upstream corpus hard-codes 2024-01-01 timestamps. Each case is")
    lines.append("shifted by one constant offset so the corpus ends five minutes before")
    lines.append("the run; query text, relative windows, steps, and directions are unchanged.")
    lines.append("")
    lines.append("## Summary")
    lines.append("")
    lines.append("| Result | Cases |")
    lines.append("| --- | ---: |")
    for name in sorted(counts):
        lines.append(f"| {name} | {counts[name]} |")
    lines.append("")
    unsupported = [result for result in results if result["kind"] == "unsupported"]
    mismatches = [result for result in results if result["kind"] == "mismatch"]
    errors = [result for result in results if result["kind"] in ("harness-error", "reference-error", "gateway-error")]
    lines.append("## Unsupported queries")
    lines.append("")
    if unsupported:
        lines.append("| Query | Gateway error |")
        lines.append("| --- | --- |")
        for result in unsupported:
            query = result["query"].replace("|", "\\|")
            detail = result["detail"].replace("|", "\\|")
            lines.append(f"| `{query}` | {detail} |")
    else:
        lines.append("None.")
    lines.append("")
    lines.append("## Mismatches")
    lines.append("")
    if mismatches:
        lines.append("| Query | Difference |")
        lines.append("| --- | --- |")
        for result in mismatches:
            query = result["query"].replace("|", "\\|")
            detail = result["detail"].replace("|", "\\|")
            lines.append(f"| `{query}` | {detail} |")
    else:
        lines.append("None.")
    lines.append("")
    lines.append("## Harness and reference errors")
    lines.append("")
    if errors:
        lines.append("| Query | Error |")
        lines.append("| --- | --- |")
        for result in errors:
            query = result["query"].replace("|", "\\|")
            detail = result["detail"].replace("|", "\\|")
            lines.append(f"| `{query}` | {detail} |")
    else:
        lines.append("None.")
    lines.append("")
    lines.append("## Reproduce")
    lines.append("")
    lines.append("```sh")
    lines.append("# Start Loki plus ClickHouse/gateway, seed both, and compare:")
    lines.append("test/compat/loki/upstream-run.sh")
    lines.append("")
    lines.append("# Regenerate this report:")
    lines.append("test/compat/loki/upstream-run.sh --report docs/logql-upstream-baseline.md")
    lines.append("")
    lines.append("# Refresh the committed corpus after changing the pinned version:")
    lines.append("test/compat/loki/upstream.sh refresh")
    lines.append("```")
    lines.append("")
    lines.append("## Limitations")
    lines.append("")
    lines.append("- The upstream generator defines queries, not a static log dataset. The")
    lines.append("  harness seeds synthetic selector/variant streams so the filters execute;")
    lines.append("  it does not reproduce Loki's multi-gigabyte generated dataset.")
    lines.append("- Log lines are deliberately small and identical across selectors. The")
    lines.append("  comparison checks result shape and values, not storage throughput.")
    lines.append("- Metric evaluation timestamps are compared at whole-millisecond")
    lines.append("  precision, which is Loki's response precision.")
    lines.append("- The reference Loki and ClickHouse fixtures contain only this run's")
    lines.append("  entries. A mismatch can be a genuine engine difference or a fixture")
    lines.append("  normalization gap; every case is listed above rather than retried.")
    lines.append("")
    with open(path, "w", encoding="utf-8") as handle:
        handle.write("\n".join(lines))


if __name__ == "__main__":
    main()
