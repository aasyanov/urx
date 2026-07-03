package syncx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FuzzMap drives a sequence of operations decoded from fuzz input against
// [Map] and an oracle built from a plain map, asserting that membership and
// length stay consistent. The map must never panic on any operation sequence.
func FuzzMap(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{0, 5, 1, 5, 2, 5, 3, 5})
	f.Add([]byte{4, 4, 4, 4})
	f.Add([]byte{5, 0, 5, 1, 0, 2})
	f.Add([]byte{6, 1, 7, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		m := NewMap[byte, byte]()
		oracle := map[byte]byte{}

		// Each pair of bytes is (op, key). op selects the operation; the
		// stored value is derived from the key to keep the oracle simple.
		for i := 0; i+1 < len(data); i += 2 {
			op, key := data[i]%8, data[i+1]
			switch op {
			case 0: // Store
				m.Store(key, key)
				oracle[key] = key
			case 1: // Delete
				m.Delete(key)
				delete(oracle, key)
			case 2: // LoadOrStore
				m.LoadOrStore(key, key)
				if _, ok := oracle[key]; !ok {
					oracle[key] = key
				}
			case 3: // LoadAndDelete
				m.LoadAndDelete(key)
				delete(oracle, key)
			case 4: // Swap
				m.Swap(key, key)
				oracle[key] = key
			case 5: // Clear
				m.Clear()
				clear(oracle)
			case 6: // CompareAndSwap (update existing only; sync.Map semantics)
				if old, ok := oracle[key]; ok {
					m.CompareAndSwap(key, old, key)
				} else {
					m.CompareAndSwap(key, 0, key)
				}
			case 7: // CompareAndDelete
				if m.CompareAndDelete(key, key) {
					delete(oracle, key)
				}
			}
		}

		if got, want := m.Len(), len(oracle); got != want {
			t.Fatalf("Len mismatch: got %d, want %d", got, want)
		}

		for k, want := range oracle {
			v, ok := m.Load(k)
			if !ok {
				t.Fatalf("key %d missing from Map", k)
			}
			if v != want {
				t.Fatalf("value mismatch for key %d: got %d, want %d", k, v, want)
			}
		}

		seen := 0
		m.Range(func(k, _ byte) bool {
			if _, ok := oracle[k]; !ok {
				t.Fatalf("Range yielded unexpected key %d", k)
			}
			seen++
			return true
		})
		if seen != len(oracle) {
			t.Fatalf("Range visited %d keys, want %d", seen, len(oracle))
		}
	})
}

// FuzzLazy drives [Lazy.Get] and [Lazy.Reset] sequences. The map must never
// panic; a latched value is stable across subsequent Gets.
func FuzzLazy(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 0})
	f.Add([]byte{1, 1, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		var calls atomic.Int64
		l, err := NewLazy(func() (byte, error) {
			return byte(calls.Add(1)), nil
		})
		require.NoError(t, err)

		for _, op := range data {
			switch op % 2 {
			case 0:
				_, _ = l.Get()
			case 1:
				l.Reset()
			}
		}

		if l.Done() {
			v1, err := l.Get()
			require.NoError(t, err)
			v2, err := l.Get()
			require.NoError(t, err)
			assert.Equal(t, v1, v2)
		}
	})
}

// FuzzGroup launches panic-safe tasks from fuzz input and verifies that
// [Group.Stats] matches the number of started goroutines.
func FuzzGroup(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 1, 2})
	f.Add([]byte{3, 3, 3})

	f.Fuzz(func(t *testing.T, data []byte) {
		g, _ := NewGroup(context.Background(), WithLimit(4))
		var launched atomic.Int64

		for _, op := range data {
			switch op % 4 {
			case 0:
				require.NoError(t, g.Go(func(context.Context) error {
					launched.Add(1)
					return nil
				}))
			case 1:
				require.NoError(t, g.Go(func(context.Context) error {
					launched.Add(1)
					return errors.New("fuzz fail")
				}))
			case 2:
				ok, err := g.TryGo(func(context.Context) error {
					launched.Add(1)
					return nil
				})
				require.NoError(t, err)
				_ = ok
			case 3:
				require.NoError(t, g.Go(func(context.Context) error {
					launched.Add(1)
					panic("fuzz panic")
				}))
			}
		}

		_ = g.Wait()
		st := g.Stats()
		if st.Started != launched.Load() {
			t.Fatalf("Started=%d, launched=%d", st.Started, launched.Load())
		}
	})
}
