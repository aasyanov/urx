package lrux

import "time"

// EvictionReason explains why an entry left the cache. It is delivered to the
// eviction callback registered via [WithOnEvict].
type EvictionReason uint8

const (
	// EvictionCapacity means the entry was removed to stay within the
	// configured capacity (least-recently-used entry dropped).
	EvictionCapacity EvictionReason = iota

	// EvictionExpired means the entry's time-to-live elapsed.
	EvictionExpired

	// EvictionDeleted means the entry was explicitly removed via
	// [Cache.Delete] or [Cache.DeleteMulti].
	EvictionDeleted

	// EvictionCleared means the entry was removed by [Cache.Clear] or
	// [Cache.Close].
	EvictionCleared

	// EvictionReplaced means an existing value was overwritten by a new
	// [Cache.Set] for the same key.
	EvictionReplaced
)

// String labels for [EvictionReason].
const (
	labelCapacity = "capacity"
	labelExpired  = "expired"
	labelDeleted  = "deleted"
	labelCleared  = "cleared"
	labelReplaced = "replaced"
	labelUnknown  = "unknown"
)

// String returns a human-readable label for the eviction reason.
func (r EvictionReason) String() string {
	switch r {
	case EvictionCapacity:
		return labelCapacity
	case EvictionExpired:
		return labelExpired
	case EvictionDeleted:
		return labelDeleted
	case EvictionCleared:
		return labelCleared
	case EvictionReplaced:
		return labelReplaced
	default:
		return labelUnknown
	}
}

// OnEvictFunc is invoked after an entry is removed from the cache. It runs
// outside the cache lock, so it may safely call back into the cache. Panics
// raised by the callback are recovered and discarded.
type OnEvictFunc[K comparable, V any] func(key K, value V, reason EvictionReason)

// Entry is an immutable snapshot of a cached key-value pair with its
// timestamps. It is returned by [Cache.GetEntry] and [Cache.Snapshot] and is
// safe to retain after the originating entry changes.
type Entry[K comparable, V any] struct {
	// Key is the entry's key.
	Key K

	// Value is the entry's value at snapshot time.
	Value V

	// CreatedAt is when the entry was first inserted.
	CreatedAt time.Time

	// AccessedAt is when the entry was last read or written.
	AccessedAt time.Time

	// ExpiresAt is the absolute expiration instant. The zero value means the
	// entry never expires.
	ExpiresAt time.Time
}

// Stats is a point-in-time snapshot of cache counters. Counters are read
// atomically; the snapshot is consistent per field but not across fields.
type Stats struct {
	// Size is the current number of entries, including expired entries that
	// have not yet been cleaned up.
	Size int `json:"size"`

	// Capacity is the configured maximum number of entries. Zero means
	// unlimited.
	Capacity int `json:"capacity"`

	// Hits is the number of successful lookups served from the cache.
	Hits uint64 `json:"hits"`

	// Misses is the number of lookups that found no live entry.
	Misses uint64 `json:"misses"`

	// Evictions is the number of entries removed due to capacity or TTL.
	Evictions uint64 `json:"evictions"`

	// HitRate is Hits / (Hits + Misses), or 0 when there were no lookups.
	HitRate float64 `json:"hit_rate"`
}

// node is an intrusive doubly-linked list element. Storing the links inside
// the node avoids the extra list.Element allocation per entry.
type node[K comparable, V any] struct {
	key        K
	value      V
	prev, next *node[K, V]
	createdAt  time.Time
	accessedAt time.Time
	expiresAt  time.Time
}

// evictEvent records a pending eviction callback to fire after the lock is
// released.
type evictEvent[K comparable, V any] struct {
	key    K
	value  V
	reason EvictionReason
}
