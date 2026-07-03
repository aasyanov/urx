package lrux

import "errors"

var (
	// ErrClosed is returned by [Cache.GetOrCompute] and
	// [ShardedCache.GetOrCompute] when the cache has been closed via
	// [Cache.Close] or [ShardedCache.Close]. All other methods become silent
	// no-ops after close (they return zero values or false without an error).
	// [Cache.Close] and [ShardedCache.Close] themselves always succeed and are
	// idempotent. It is safe to compare with == or [errors.Is].
	ErrClosed = errors.New("lrux: cache is closed")

	// ErrNotFound is a sentinel compute functions may return from
	// [Cache.GetOrCompute] to signal that a value could not be produced.
	// lrux propagates the error to the caller and does not cache a result.
	// It is safe to compare with == or [errors.Is].
	ErrNotFound = errors.New("lrux: key not found")
)
