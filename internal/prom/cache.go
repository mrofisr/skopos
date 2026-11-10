package prom

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jawaracloud/skopos/internal/chclient"
	"github.com/jawaracloud/skopos/internal/plan"
	"github.com/jawaracloud/skopos/internal/ttlcache"
)

const (
	defaultMetadataTTL = 30 * time.Second
	discoveryCacheSize = 4096
	nonEmptyCacheSize  = 128
	metadataCacheSize  = 4096
)

// metricCache caches the answers that do not change per query: which tables
// hold a metric, whether a table has any rows, and metadata.
type metricCache struct {
	fetcher    *ClickHouseFetcher
	discovery  *ttlcache.Cache[string, []tableKind]
	nonEmpty   *ttlcache.Cache[string, bool]
	metadata   *ttlcache.Cache[string, map[string]Metadata]
	labelNames *ttlcache.Cache[labelNamesKey, []string]

	labelMu       sync.Mutex
	labelRequests map[labelNamesKey]labelNamesRequest
}

type labelNamesKey struct {
	Selector string
	Start    int64
	End      int64
	Limit    int
}

type labelNamesRequest struct {
	selector Selector
	start    time.Time
	end      time.Time
	limit    int
}

func (f *ClickHouseFetcher) cacheFor() *metricCache {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	if f.cache != nil {
		return f.cache
	}
	ttl := f.MetadataTTL
	if ttl <= 0 {
		ttl = defaultMetadataTTL
	}
	clock := f.Now
	if clock == nil {
		clock = time.Now
	}
	discovery, _ := ttlcache.New(ttlcache.Options[string, []tableKind]{
		TTL: ttl, MaxEntries: discoveryCacheSize, Clock: clock, Load: f.probeTables,
	})
	nonEmpty, _ := ttlcache.New(ttlcache.Options[string, bool]{
		TTL: ttl, MaxEntries: nonEmptyCacheSize, Clock: clock, Load: f.tableHasData,
	})
	metadata, _ := ttlcache.New(ttlcache.Options[string, map[string]Metadata]{
		TTL: ttl, MaxEntries: metadataCacheSize, Clock: clock, Load: f.loadMetadata,
	})
	cache := &metricCache{
		fetcher: f, discovery: discovery, nonEmpty: nonEmpty, metadata: metadata,
		labelRequests: make(map[labelNamesKey]labelNamesRequest),
	}
	labelNames, _ := ttlcache.New(ttlcache.Options[labelNamesKey, []string]{
		TTL: ttl, MaxEntries: metadataCacheSize, Clock: clock, Load: cache.loadLabelNames,
	})
	cache.labelNames = labelNames
	f.cache = cache
	return f.cache
}

// tableHasData probes one table with LIMIT 1 and selects only MetricName.
func (f *ClickHouseFetcher) tableHasData(ctx context.Context, table string) (bool, error) {
	query := plan.Query{
		Signal: plan.SignalMetrics,
		Table:  table,
		Select: []plan.SelectItem{{Expr: plan.Col("MetricName")}},
		Limit:  1,
	}
	emitted, err := f.Emitter.Emit(query)
	if err != nil {
		return false, fmt.Errorf("prom: build emptiness query: %w", err)
	}
	result, err := f.Client.Query(chclient.WithHead(ctx, "prom"), emitted.SQL, emitted.Params)
	if err != nil {
		return false, fmt.Errorf("prom: emptiness query %s: %w", table, err)
	}
	return result.Len() > 0, nil
}

func cloneMetadata(source map[string]Metadata) map[string]Metadata {
	out := make(map[string]Metadata, len(source))
	for name, metadata := range source {
		out[name] = metadata
	}
	return out
}

func (c *metricCache) loadLabelNames(ctx context.Context, key labelNamesKey) ([]string, error) {
	c.labelMu.Lock()
	request := c.labelRequests[key]
	c.labelMu.Unlock()
	return c.fetcher.loadLabelNames(ctx, request.selector, request.start, request.end, request.limit)
}
