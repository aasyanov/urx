package healthx

import "time"

// defaultCheckTimeout bounds each individual component check inside
// [Checker.Readiness] when [WithTimeout] is not supplied.
const defaultCheckTimeout = 5 * time.Second

// Option configures a [Checker] created with [New].
type Option func(*config)

type config struct {
	checkTimeout time.Duration
}

func newConfig(opts []Option) config {
	cfg := config{checkTimeout: defaultCheckTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithTimeout sets the per-check timeout applied to every component check in
// [Checker.Readiness]. Default: 5s. A zero or negative duration is ignored
// and the default is kept.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.checkTimeout = d
		}
	}
}
