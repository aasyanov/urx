package lrux

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzCache_SetGet asserts that any value stored under a key is retrievable
// while the cache has spare capacity, and that the cache never panics on
// arbitrary string keys.
func FuzzCache_SetGet(f *testing.F) {
	f.Add("key", 1)
	f.Add("", 0)
	f.Add("unicode-Ключ-🔑", -42)

	f.Fuzz(func(t *testing.T, key string, value int) {
		c := New[string, int](WithCapacity[string, int](8))
		defer c.Close()

		c.Set(key, value)
		got, ok := c.Get(key)
		require.True(t, ok, "key %q absent immediately after Set", key)
		require.Equal(t, value, got)

		require.True(t, c.Delete(key), "Delete(%q) reported missing key", key)
		_, ok = c.Get(key)
		require.False(t, ok, "key %q present after Delete", key)
	})
}

// FuzzShardedCache_Distribution asserts the sharded cache never panics and
// keeps keys retrievable for arbitrary keys under an unbounded capacity.
func FuzzShardedCache_Distribution(f *testing.F) {
	f.Add("a", "b", "c")
	f.Add("", "", "")
	f.Add("x", "x", "x")

	f.Fuzz(func(t *testing.T, k1, k2, k3 string) {
		c := NewSharded[string, int](WithShardCount[string, int](4))
		defer c.Close()

		keys := []string{k1, k2, k3}
		for i, k := range keys {
			c.Set(k, i)
		}
		for _, k := range keys {
			_, ok := c.Get(k)
			require.True(t, ok, "key %q absent after Set in sharded cache", k)
		}
	})
}

// FuzzKeyString asserts keyString never panics and is deterministic for
// arbitrary string inputs.
func FuzzKeyString(f *testing.F) {
	f.Add("plain")
	f.Add("")
	f.Add("with\x00null")

	f.Fuzz(func(t *testing.T, s string) {
		a := keyString(s)
		b := keyString(s)
		require.Equal(t, a, b, "keyString(%q) not deterministic", s)
		require.Equal(t, s, a, "keyString(%q) should be identity for strings", s)
	})
}
