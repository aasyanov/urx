package syncx

import "sync"

// Map is a generic, type-safe concurrent map. It is a thin wrapper around
// [sync.Map] that provides compile-time type safety.
type Map[K comparable, V any] struct {
	m   sync.Map
	len int64
	mu  sync.Mutex
}

// NewMap creates an empty [Map].
func NewMap[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{}
}

// Load returns the value stored for key, or the zero value and false if
// no entry is present.
func (m *Map[K, V]) Load(key K) (V, bool) {
	val, ok := m.m.Load(key)
	if !ok {
		var zero V
		return zero, false
	}
	return val.(V), true
}

// Store sets the value for key.
func (m *Map[K, V]) Store(key K, value V) {
	m.mu.Lock()
	if _, loaded := m.m.Swap(key, value); !loaded {
		m.len++
	}
	m.mu.Unlock()
}

// Delete removes the entry for key. It is a no-op if the key does not exist.
func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()
	if _, loaded := m.m.LoadAndDelete(key); loaded {
		m.len--
	}
	m.mu.Unlock()
}

// LoadOrStore returns the existing value for key if present. Otherwise, it
// stores and returns the given value. The loaded result is true if the value
// was loaded, false if stored.
func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool) {
	m.mu.Lock()
	actual, loaded := m.m.LoadOrStore(key, value)
	if !loaded {
		m.len++
	}
	m.mu.Unlock()
	return actual.(V), loaded
}

// Range calls fn sequentially for each key-value pair. If fn returns false,
// iteration stops.
func (m *Map[K, V]) Range(fn func(key K, value V) bool) {
	m.m.Range(func(k, v any) bool {
		return fn(k.(K), v.(V))
	})
}

// Len returns the number of entries in the map.
func (m *Map[K, V]) Len() int {
	m.mu.Lock()
	n := m.len
	m.mu.Unlock()
	return int(n)
}
