package sqlgen

import "time"

// bucketWidth is the leading component of every metrics sort key in
// schema.sql. Keep it in step with ORDER BY (toStartOfFiveMinutes(TimeUnix),
// ...): the predicate only prunes granules when it matches the key exactly.
const bucketWidth = 5 * time.Minute

// bucketBounds returns the inclusive five-minute bucket range that covers the
// query window. The caller keeps the real time predicate; this range is a
// superset filter that lets ClickHouse skip granules that cannot hold a row in
// the window. An exclusive end excludes the boundary instant itself, so the
// last bucket is the one holding end-1ns.
func bucketBounds(start, end time.Time, endInclusive bool) (from, to time.Time) {
	from = start.Truncate(bucketWidth)
	last := end
	if !endInclusive {
		last = end.Add(-time.Nanosecond)
	}
	return from, last.Truncate(bucketWidth)
}
