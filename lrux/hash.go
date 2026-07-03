package lrux

import (
	"fmt"
	"hash/maphash"
	"strconv"
)

// newHasher builds a hash function for keys of type K used to select a shard.
// It uses [maphash.Comparable] with a per-cache random seed for DoS resistance
// and an even shard distribution. The hash works for any comparable key type
// and allocates nothing on the hot path.
func newHasher[K comparable]() func(K) uint64 {
	seed := maphash.MakeSeed()
	return func(key K) uint64 {
		return maphash.Comparable(seed, key)
	}
}

// keyString renders a key as a singleflight deduplication key. Primitive
// types use strconv to avoid fmt overhead on the hot path.
func keyString[K comparable](key K) string {
	switch k := any(key).(type) {
	case string:
		return k
	case int:
		return strconv.FormatInt(int64(k), 10)
	case int8:
		return strconv.FormatInt(int64(k), 10)
	case int16:
		return strconv.FormatInt(int64(k), 10)
	case int32:
		return strconv.FormatInt(int64(k), 10)
	case int64:
		return strconv.FormatInt(k, 10)
	case uint:
		return strconv.FormatUint(uint64(k), 10)
	case uint8:
		return strconv.FormatUint(uint64(k), 10)
	case uint16:
		return strconv.FormatUint(uint64(k), 10)
	case uint32:
		return strconv.FormatUint(uint64(k), 10)
	case uint64:
		return strconv.FormatUint(k, 10)
	case uintptr:
		return strconv.FormatUint(uint64(k), 10)
	case float32:
		return strconv.FormatFloat(float64(k), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(k, 'g', -1, 64)
	case bool:
		if k {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(key)
	}
}

// nextPow2 rounds n up to the nearest power of two, with a floor of 1.
func nextPow2(n int) int {
	count := 1
	for count < n {
		count <<= 1
	}
	return count
}
