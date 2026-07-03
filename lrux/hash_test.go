package lrux

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewHasher_PrimitiveTypes(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		h := newHasher[string]()
		assert.Equal(t, h("x"), h("x"))
		assert.NotEqual(t, h("x"), h("y"))
	})
	t.Run("int", func(t *testing.T) {
		h := newHasher[int]()
		assert.Equal(t, h(7), h(7))
		assert.NotEqual(t, h(7), h(8))
	})
	t.Run("int8", func(t *testing.T) {
		h := newHasher[int8]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("int16", func(t *testing.T) {
		h := newHasher[int16]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("int32", func(t *testing.T) {
		h := newHasher[int32]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("int64", func(t *testing.T) {
		h := newHasher[int64]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uint", func(t *testing.T) {
		h := newHasher[uint]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uint8", func(t *testing.T) {
		h := newHasher[uint8]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uint16", func(t *testing.T) {
		h := newHasher[uint16]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uint32", func(t *testing.T) {
		h := newHasher[uint32]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uint64", func(t *testing.T) {
		h := newHasher[uint64]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("uintptr", func(t *testing.T) {
		h := newHasher[uintptr]()
		assert.NotEqual(t, h(1), h(2))
	})
	t.Run("float32", func(t *testing.T) {
		h := newHasher[float32]()
		assert.NotEqual(t, h(1.5), h(2.5))
	})
	t.Run("float64", func(t *testing.T) {
		h := newHasher[float64]()
		assert.NotEqual(t, h(1.5), h(2.5))
	})
	t.Run("bool", func(t *testing.T) {
		h := newHasher[bool]()
		assert.NotEqual(t, h(true), h(false))
	})
	t.Run("struct fallback", func(t *testing.T) {
		type composite struct {
			A int
			B string
		}
		h := newHasher[composite]()
		assert.Equal(t, h(composite{1, "x"}), h(composite{1, "x"}))
		assert.NotEqual(t, h(composite{1, "x"}), h(composite{2, "y"}))
	})
}

func TestKeyString_PrimitiveTypes(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"string", keyString("hello"), "hello"},
		{"int", keyString(42), "42"},
		{"int8", keyString(int8(8)), "8"},
		{"int16", keyString(int16(16)), "16"},
		{"int32", keyString(int32(32)), "32"},
		{"int64", keyString(int64(64)), "64"},
		{"uint", keyString(uint(1)), "1"},
		{"uint8", keyString(uint8(2)), "2"},
		{"uint16", keyString(uint16(3)), "3"},
		{"uint32", keyString(uint32(4)), "4"},
		{"uint64", keyString(uint64(5)), "5"},
		{"uintptr", keyString(uintptr(6)), "6"},
		{"float32", keyString(float32(1.5)), "1.5"},
		{"float64", keyString(2.5), "2.5"},
		{"bool true", keyString(true), "true"},
		{"bool false", keyString(false), "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.got)
		})
	}
}

func TestKeyString_StructFallback(t *testing.T) {
	type pair struct{ A, B int }
	assert.Equal(t, keyString(pair{1, 2}), keyString(pair{1, 2}))
	assert.NotEqual(t, keyString(pair{1, 2}), keyString(pair{3, 4}))
}

func TestNewHasher_StableAcrossCalls(t *testing.T) {
	h := newHasher[int]()
	first := h(12345)
	for range 100 {
		assert.Equal(t, first, h(12345))
	}
}

func TestNewHasher_DistinctSeedsDiffer(t *testing.T) {
	h1 := newHasher[string]()
	h2 := newHasher[string]()
	// Independent seeds make collisions on the same key vanishingly unlikely.
	assert.NotEqual(t, h1("same-key"), h2("same-key"))
}
