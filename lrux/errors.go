package lrux

import "errors"

var (
	// ErrClosed is returned by mutating operations attempted on a cache that
	// has already been closed via [Cache.Close] or [ShardedCache.Close].
	// It is safe to compare with == or [errors.Is].
	ErrClosed = errors.New("lrux: cache is closed")

	// ErrNotFound is returned by [Cache.GetOrComputeCtx] and related compute
	// methods when the key is absent and the compute function reports that no
	// value could be produced without itself returning a more specific error.
	// It is safe to compare with == or [errors.Is].
	ErrNotFound = errors.New("lrux: key not found")
)
