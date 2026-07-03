package syncx

import (
	"sync"
	"sync/atomic"
)

// Map is a generic, type-safe concurrent map. It is a thin wrapper around
// [sync.Map] that adds compile-time key/value typing and an O(1) [Map.Len].
// It is safe for concurrent use from multiple goroutines.
//
// Like [sync.Map], Map is optimized for two cases: keys that are written once
// but read many times, and disjoint key sets across goroutines. For workloads
// dominated by writes to a shared key set, a plain map guarded by a
// [sync.Mutex] may perform better.
//
// The zero Map is ready to use; [NewMap] is provided for symmetry with the
// rest of the package.
type Map[K comparable, V any] struct {
	m   sync.Map
	len atomic.Int64
}

// NewMap creates an empty [Map].
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{}
}

// Load returns the value stored for key, or the zero value and false if no
// entry is present.
func (m *Map[K, V]) Load(key K) (V, bool) {
	val, ok := m.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

// Store sets the value for key, overwriting any existing value.
func (m *Map[K, V]) Store(key K, value V) {
	if _, loaded := m.m.Swap(key, value); !loaded {
		m.len.Add(1)
	}
}

// Swap stores value for key and returns the previous value if one was
// present. The loaded result is true if an existing value was replaced.
func (m *Map[K, V]) Swap(key K, value V) (previous V, loaded bool) {
	prev, loaded := m.m.Swap(key, value)
	if !loaded {
		m.len.Add(1)
		var zero V
		return zero, false
	}
	return prev.(V), true
}

// Delete removes the entry for key. It is a no-op if the key is absent.
func (m *Map[K, V]) Delete(key K) {
	if _, loaded := m.m.LoadAndDelete(key); loaded {
		m.len.Add(-1)
	}
}

// LoadAndDelete deletes the value for key, returning the previous value if
// any. The loaded result is true if the key was present.
func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	val, loaded := m.m.LoadAndDelete(key)
	if !loaded {
		var zero V
		return zero, false
	}
	m.len.Add(-1)
	return val.(V), true
}

// LoadOrStore returns the existing value for key if present. Otherwise it
// stores and returns value. The loaded result is true if the value was
// loaded, false if stored.
func (m *Map[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	val, loaded := m.m.LoadOrStore(key, value)
	if !loaded {
		m.len.Add(1)
	}
	return val.(V), loaded
}

// Range calls fn sequentially for each key-value pair. If fn returns false,
// iteration stops. Range does not necessarily correspond to any consistent
// snapshot of the map's contents (see [sync.Map.Range]).
func (m *Map[K, V]) Range(fn func(key K, value V) bool) {
	m.m.Range(func(k, v any) bool {
		return fn(k.(K), v.(V))
	})
}

// Len returns the number of entries currently in the map.
func (m *Map[K, V]) Len() int {
	return int(m.len.Load())
}

// Clear removes all entries from the map and resets [Map.Len] to zero.
// Concurrent stores may add entries while Clear runs; those entries remain
// after Clear returns and Len is adjusted by the usual store/delete paths.
func (m *Map[K, V]) Clear() {
	m.m.Clear()
	m.len.Store(0)
}
