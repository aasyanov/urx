package signalx

import (
	"os"
	"syscall"
	"time"
)

const (
	// defaultShutdownTimeout bounds the total time all shutdown hooks may
	// take inside [Wait]. It is deliberately generous: most services drain
	// connections and flush buffers well within this window.
	defaultShutdownTimeout = 15 * time.Second
)

// defaultSignals are the OS signals [Trap] traps when no explicit signals
// are supplied: interrupt (Ctrl-C) and terminate.
var defaultSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// Option configures the behavior of [WaitWith].
type Option func(*config)

type config struct {
	timeout time.Duration
}

// WithTimeout sets the maximum total duration all shutdown hooks may take.
// Default: 15s. A zero or negative duration disables the timeout, letting
// hooks run until completion.
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

func newConfig(opts []Option) config {
	cfg := config{
		timeout: defaultShutdownTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
