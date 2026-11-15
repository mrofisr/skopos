// k6 load test for the Skopos query gateway.
//
// This is a manual throughput/latency test, not a CI correctness gate. It
// exercises the public Prometheus, Tempo, and Loki-compatible query paths with
// live OTel data in ClickHouse.
//
// See README.md for smoke and stress examples.

import http from 'k6/http';
import { check, sleep } from 'k6';

const BASE_URL = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/+$/, '');
const AUTH_TOKEN = __ENV.AUTH_TOKEN || '';
const SMOKE = (__ENV.SMOKE || 'false').toLowerCase() === 'true';

function envInt(name, fallback) {
  const value = Number(__ENV[name]);
  return Number.isFinite(value) && value > 0 ? value : fallback;
}

const VUS = envInt('VUS', 5);
const SLEEP_SECONDS = Number(__ENV.SLEEP || 0.5);
const P95_MS = envInt('P95_MS', 1500);

const thresholds = {
  checks: ['rate>0.99'],
  http_req_failed: ['rate<0.01'],
  http_req_duration: [`p(95)<${P95_MS}`],
};

export const options = SMOKE
  ? {
      vus: 1,
      iterations: envInt('ITERATIONS', 3),
      thresholds,
    }
  : {
      scenarios: {
        mixed: {
          executor: 'ramping-vus',
          startVUs: 1,
          stages: [
            { duration: __ENV.RAMP_UP || '30s', target: VUS },
            { duration: __ENV.HOLD || '1m', target: VUS },
            { duration: __ENV.RAMP_DOWN || '15s', target: 0 },
          ],
          gracefulRampDown: '10s',
        },
      },
      thresholds,
    };

// The query names use the OTel metric names exposed by the Skopos gateway.
// They intentionally omit the "_seconds" suffix used by some Prometheus
// exporters; see docs/skopos-on-k0s.md.
const QUERIES = [
  {
    name: 'prom_vector',
    kind: 'prom',
    path: '/api/v1/query',
    params: { query: 'vector(42)' },
  },
  {
    name: 'prom_rate',
    kind: 'prom',
    path: '/api/v1/query',
    params: { query: 'sum(rate(http_server_request_duration_count[5m]))' },
  },
  {
    name: 'prom_by_service',
    kind: 'prom',
    path: '/api/v1/query',
    params: {
      query: 'sum by (service_name) (rate(http_server_request_duration_count[5m]))',
    },
  },
  {
    name: 'prom_p95',
    kind: 'prom',
    path: '/api/v1/query',
    params: {
      query:
        'histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_bucket[5m])))',
    },
  },
  {
    name: 'prom_range',
    kind: 'prom',
    path: '/api/v1/query_range',
    params: {
      query: 'sum(rate(http_server_request_duration_count[5m]))',
      step: '30s',
    },
    minutes: 5,
  },
  {
    name: 'prom_series',
    kind: 'prom',
    path: '/api/v1/series',
    params: {
      'match[]': 'http_server_request_duration_count{service_name="whoami"}',
    },
    minutes: 60,
  },
  {
    name: 'prom_label_values',
    kind: 'prom',
    path: '/api/v1/label/__name__/values',
    params: {},
  },
  {
    name: 'tempo_search',
    kind: 'tempo',
    path: '/api/search',
    params: {
      q: '{ service.name = "whoami" }',
      limit: '10',
    },
    minutes: 60,
  },
  {
    name: 'loki_labels',
    kind: 'loki',
    path: '/loki/api/v1/labels',
    params: {},
  },
];

function buildURL(query) {
  const params = Object.assign({}, query.params);
  if (query.minutes) {
    const now = Math.floor(Date.now() / 1000);
    params.start = String(now - query.minutes * 60);
    params.end = String(now);
  }
  const pairs = Object.keys(params).map(
    (key) => `${encodeURIComponent(key)}=${encodeURIComponent(params[key])}`
  );
  const suffix = pairs.length ? `?${pairs.join('&')}` : '';
  return `${BASE_URL}${query.path}${suffix}`;
}

function isSuccess(response, kind) {
  let body;
  try {
    body = response.json();
  } catch (_) {
    return false;
  }
  if (!body || body.status === 'error') {
    return false;
  }
  if (kind === 'tempo') {
    return Array.isArray(body.traces) || Array.isArray(body.resourceSpans);
  }
  return body.status === 'success';
}

export function setup() {
  const response = http.get(`${BASE_URL}/readyz`, {
    tags: { name: 'readyz' },
  });
  if (response.status !== 200) {
    throw new Error(`Skopos gateway is not ready: HTTP ${response.status}`);
  }
}

export default function () {
  const query = QUERIES[(__VU + __ITER) % QUERIES.length];
  const headers = { Accept: 'application/json' };
  if (AUTH_TOKEN) {
    headers.Authorization = `Bearer ${AUTH_TOKEN}`;
  }

  const response = http.get(buildURL(query), {
    headers,
    tags: { name: query.name, kind: query.kind },
  });

  check(response, {
    'status is 200': (res) => res.status === 200,
    'valid gateway response': (res) => isSuccess(res, query.kind),
  });

  sleep(SLEEP_SECONDS);
}
