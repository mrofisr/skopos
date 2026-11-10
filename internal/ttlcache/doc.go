// Package ttlcache is a bounded TTL cache with single-flight loading and an
// injectable clock. It is the shared cache seam for metadata, emptiness, and
// label lookups; callers own the loader and the key shape.
package ttlcache
