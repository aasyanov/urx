package hedgex

import "time"

const (
	// DefaultMaxParallel is the maximum number of in-flight copies (original +
	// hedges) applied when [WithMaxParallel] is not supplied.
	DefaultMaxParallel = 3

	// DefaultDelay is the stagger between launching successive copies applied
	// when [WithDelay] is not supplied. The first hedge starts one delay after
	// the original, the second two delays after, and so on (until [DefaultMaxDelay]).
	DefaultDelay = 100 * time.Millisecond

	// DefaultMaxDelay caps the total stagger window applied when [WithMaxDelay]
	// is not supplied. Copies scheduled past this point are spread thinly so a
	// large MaxParallel does not collapse into a synchronized burst.
	DefaultMaxDelay = 1 * time.Second

	// minParallel is the floor [New] enforces: a non-positive MaxParallel
	// degrades to a single copy (no hedging) rather than an empty fan-out.
	minParallel = 1

	// spreadDivisor derives the tail stagger from the base delay once MaxDelay
	// is hit: copies past the cap are launched spreadDelay = delay/spreadDivisor
	// apart so they do not all fire at MaxDelay simultaneously.
	spreadDivisor = 4

	// minSpread is the floor for the tail stagger, guarding against a zero
	// spread when delay/spreadDivisor rounds down below one tick.
	minSpread = time.Millisecond
)

// Option configures a [Hedger] created by [New].
type Option func(*config)

// config holds resolved hedger parameters.
type config struct {
	maxParallel int
	delay       time.Duration
	maxDelay    time.Duration
	onHedge     func(attempt int)
	op          string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the parallelism floor and the delay/maxDelay relationship
// fixed up last so an invalid option can never produce an unusable hedger.
func newConfig(opts []Option) config {
	cfg := config{
		maxParallel: DefaultMaxParallel,
		delay:       DefaultDelay,
		maxDelay:    DefaultMaxDelay,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxParallel < minParallel {
		cfg.maxParallel = minParallel
	}
	if cfg.maxDelay < cfg.delay {
		cfg.maxDelay = cfg.delay
	}
	return cfg
}

// opOrDefault returns the configured operation name, or [opExecute] when none
// was set.
func (c config) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opExecute
}

// WithMaxParallel sets the maximum number of concurrent copies (the original
// request plus hedges). Default: [DefaultMaxParallel]. Values <= 0 are ignored
// and a final value below 1 is floored to 1 (which disables hedging).
func WithMaxParallel(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxParallel = n
		}
	}
}

// WithDelay sets the base stagger between launching successive copies. The next
// copy fires when its scheduled delay elapses if earlier copies have not yet
// succeeded; if every in-flight copy finishes without a win, the next copy
// launches immediately rather than waiting out the remaining delay. Default:
// [DefaultDelay]. Values <= 0 are ignored.
func WithDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.delay = d
		}
	}
}

// WithMaxDelay caps the total stagger window. Copies scheduled past MaxDelay are
// spread evenly (delay/4, floored at 1ms apart) to avoid a synchronized burst.
// Default: [DefaultMaxDelay]. Values <= 0 are ignored; a MaxDelay below the
// per-copy delay is raised to the delay so the schedule stays monotonic.
func WithMaxDelay(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxDelay = d
		}
	}
}

// WithOnHedge registers a callback invoked just before each hedge copy is
// launched, with the 1-based attempt number (2 for the first hedge, 3 for the
// second, ...). It runs asynchronously under panic recovery so a slow or
// panicking hook never stalls or crashes the dispatch loop. Default: none.
func WithOnHedge(fn func(attempt int)) Option {
	return func(c *config) { c.onHedge = fn }
}

// WithOp sets the logical operation name attached to panic reports raised by a
// hedged function (e.g. "api.read", "db.query"). Default: "hedgex.Execute".
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}
