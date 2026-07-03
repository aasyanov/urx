package syncx

import (
	"testing"
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

	f.Fuzz(func(t *testing.T, data []byte) {
		m := NewMap[byte, byte]()
		oracle := map[byte]byte{}

		// Each pair of bytes is (op, key). op selects the operation; the
		// stored value is derived from the key to keep the oracle simple.
		for i := 0; i+1 < len(data); i += 2 {
			op, key := data[i]%6, data[i+1]
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

		// Range must visit exactly the oracle's keys.
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
