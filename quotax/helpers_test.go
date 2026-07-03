package quotax

import (
	"context"
	"sync/atomic"
)

// cancelAfterCtx returns a context whose [context.Context.Err] becomes
// non-nil after the n-th call, enabling deterministic tests of the wait-loop
// re-check points without relying on real timer scheduling.
type cancelAfterCtx struct {
	context.Context
	calls atomic.Int32
	after int32
	err   error
}

func (c *cancelAfterCtx) Err() error {
	if c.Context == nil {
		c.Context = context.Background()
	}
	if c.err == nil {
		c.err = context.Canceled
	}
	if c.calls.Add(1) >= c.after {
		return c.err
	}
	return c.Context.Err()
}
