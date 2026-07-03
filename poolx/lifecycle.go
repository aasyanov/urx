package poolx

import (
	"context"
	"time"
)

// closeSignal is a minimal [context.Context] implementation that cancels when
// done is closed. It avoids storing a [context.Context] in long-lived structs
// while still letting flush callbacks observe [Batch.Close].
type closeSignal struct {
	done <-chan struct{}
}

// Deadline reports that no deadline is set.
func (c closeSignal) Deadline() (time.Time, bool) { return time.Time{}, false }

// Done returns the channel closed when the batch shuts down.
func (c closeSignal) Done() <-chan struct{} { return c.done }

// Err returns [context.Canceled] after shutdown.
func (c closeSignal) Err() error {
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

// Value returns nil for all keys.
func (c closeSignal) Value(key any) any { return nil }
