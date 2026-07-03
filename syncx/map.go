package syncx

import (
	"sync"
	"sync/atomic"
)

// Map is a generic, type-safe concurrent map. It is a thin wrapper around
// [sync.Map] that adds compile-time key/value typing and an O(1) [Map.Len].
// It is safe for concurrent use from multiple goroutines.
//
// Mutating operations serialize length accounting through an internal mutex
// so [Map.Len] matches live entries once each mutation completes, including
// when [Map.Clear] runs concurrently with [Map.Store] or [Map.Delete]. Reads
// ([Map.Load], [Map.Range], [Map.Len]) do not take the mutex; a concurrent
// [Map.Load] may therefore observe an entry before [Map.Len] increments.
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
	// mu serializes length updates with [Map.Clear] so Len stays consistent
	// when Clear runs concurrently with Store, Delete, and related ops.
	mu sync.Mutex
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, loaded := m.m.Swap(key, value); !loaded {
		m.len.Add(1)
	}
}

// Swap stores value for key and returns the previous value if one was
// present. The loaded result is true if an existing value was replaced.
func (m *Map[K, V]) Swap(key K, value V) (previous V, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, loaded := m.m.LoadAndDelete(key); loaded {
		m.len.Add(-1)
	}
}

// LoadAndDelete deletes the value for key, returning the previous value if
// any. The loaded result is true if the key was present.
func (m *Map[K, V]) LoadAndDelete(key K) (value V, loaded bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
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
	m.mu.Lock()
	defer m.mu.Unlock()
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

// Len returns an O(1) snapshot of the entry count maintained by mutating
// operations. It may briefly lag [Map.Load] during concurrent writes.
func (m *Map[K, V]) Len() int {
	return int(m.len.Load())
}

// CompareAndSwap swaps value for key only if the map holds old. It reports
// whether the swap happened. Like [sync.Map.CompareAndSwap], it updates
// existing entries only and does not insert absent keys or change [Map.Len].
func (m *Map[K, V]) CompareAndSwap(key K, old, new V) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.m.CompareAndSwap(key, old, new)
}

// CompareAndDelete deletes the entry for key only if the map holds old. It
// reports whether the entry was removed. A successful delete decrements
// [Map.Len].
func (m *Map[K, V]) CompareAndDelete(key K, old V) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := m.m.CompareAndDelete(key, old)
	if deleted {
		m.len.Add(-1)
	}
	return deleted
}

// Clear removes all entries from the map and resets [Map.Len] to zero.
// Entries added concurrently while Clear holds its lock remain after Clear
// returns; [Map.Len] reflects the live count via the usual store/delete paths.
func (m *Map[K, V]) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.m.Clear()
	m.len.Store(0)
}
