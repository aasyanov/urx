// Package healthx provides a concurrent health-check registry for
// Kubernetes-style liveness and readiness probes.
//
// A [Checker] aggregates named component checks and exposes them through
// [Checker.Liveness], [Checker.Readiness], and ready-made [net/http]
// handlers. Liveness reflects only a manual up/down flag and is therefore
// cheap; readiness runs every registered check concurrently, each under a
// per-check timeout and panic recovery, and aggregates the results into a
// [Report].
//
//	hc := healthx.New(healthx.WithTimeout(3 * time.Second))
//	hc.Register("postgres", func(ctx context.Context) error {
//	    return db.PingContext(ctx)
//	})
//	mux := http.NewServeMux()
//	hc.RegisterHandlers(mux) // /healthz, /livez, /readyz
//
// # Zero Dependencies
//
// healthx depends only on the Go standard library and the urx panix package
// for panic-safe check execution.
package healthx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opCheck is the panic-recovery op-label prefix for component checks.
const opCheck = "healthx.Checker.check"

// zeroDuration is the [Report.Duration] reported by [Checker.Liveness], which
// runs no checks.
const zeroDuration = "0s"

// collectGrace is the slack added to the per-check timeout when bounding
// result collection in runChecks. A context-respecting check returns at its
// own deadline; this margin lets that result arrive before runChecks gives up
// and force-marks the component as timed out, so the grace path triggers only
// for checks that genuinely ignore their context.
const collectGrace = 100 * time.Millisecond

// namedCheck pairs a component name with its health-check function.
type namedCheck struct {
	name  string
	check func(ctx context.Context) error
}

// Checker is a registry of named health checks that produces liveness and
// readiness [Report] values. It is safe for concurrent use.
//
// Create with [New], register checks with [Checker.Register], and expose the
// probes with [Checker.RegisterHandlers] or the individual handler methods.
type Checker struct {
	cfg config

	mu     sync.RWMutex
	checks []namedCheck

	down atomic.Bool

	readinessChecks   atomic.Uint64
	readinessFailures atomic.Uint64
}

// New creates a [Checker] with the given options.
// Default configuration: 5s per-check timeout.
func New(opts ...Option) *Checker {
	return &Checker{cfg: newConfig(opts)}
}

// Register adds a named component check. Registering the same name more than
// once keeps every registration; the readiness report key is the name, so a
// later registration's result overwrites an earlier one in the [Report].
// It is safe for concurrent use. Panics if name is empty or check is nil.
func (c *Checker) Register(name string, check func(ctx context.Context) error) {
	if name == "" {
		panic("healthx: Register name must not be empty")
	}
	if check == nil {
		panic("healthx: Register check must not be nil for " + name)
	}
	c.mu.Lock()
	c.checks = append(c.checks, namedCheck{name: name, check: check})
	c.mu.Unlock()
}

// MarkDown manually marks the system as down so that [Checker.Liveness] and
// [Checker.Readiness] report [StatusDown] regardless of component checks.
// Use it during graceful shutdown to fail readiness before the process exits.
func (c *Checker) MarkDown() { c.down.Store(true) }

// MarkUp clears the flag set by [Checker.MarkDown], restoring normal probe
// behavior.
func (c *Checker) MarkUp() { c.down.Store(false) }

// IsDown reports whether the system has been manually marked down via
// [Checker.MarkDown].
func (c *Checker) IsDown() bool { return c.down.Load() }

// Liveness returns a [Report] reflecting only the manual up/down flag. It
// runs no component checks and never blocks, so it is safe to wire to a
// frequently polled liveness probe. The context is accepted for interface
// symmetry but unused.
func (c *Checker) Liveness(_ context.Context) Report {
	status := StatusUp
	if c.down.Load() {
		status = StatusDown
	}
	return Report{Status: status, Duration: zeroDuration}
}

// Readiness runs every registered check concurrently and returns an aggregate
// [Report]. Each check runs under the configured per-check timeout (see
// [WithTimeout]) and under panic recovery, so a slow or panicking check
// cannot block or crash the probe. A nil ctx is treated as
// [context.Background].
//
// The overall status is [StatusDown] if the system is marked down or any
// component check fails; otherwise [StatusUp]. When marked down the component
// checks are skipped entirely.
func (c *Checker) Readiness(ctx context.Context) Report {
	if ctx == nil {
		ctx = context.Background()
	}
	c.readinessChecks.Add(1)
	start := time.Now()

	if c.down.Load() {
		c.readinessFailures.Add(1)
		return Report{Status: StatusDown, Duration: time.Since(start).String()}
	}

	checks := c.snapshot()
	if len(checks) == 0 {
		return Report{Status: StatusUp, Duration: time.Since(start).String()}
	}

	components := c.runChecks(ctx, checks)

	overall := StatusUp
	for _, cs := range components {
		if cs.Status == StatusDown {
			overall = StatusDown
			break
		}
	}
	if overall == StatusDown {
		c.readinessFailures.Add(1)
	}

	return Report{
		Status:     overall,
		Components: components,
		Duration:   time.Since(start).String(),
	}
}

// snapshot copies the registered checks under the read lock so the checks run
// without holding it.
func (c *Checker) snapshot() []namedCheck {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]namedCheck, len(c.checks))
	copy(out, c.checks)
	return out
}

// checkResult pairs a component name with its collected probe outcome.
type checkResult struct {
	name   string
	status ComponentStatus
}

// drainCheckResults records every result already waiting in results. It is
// called when the collection deadline fires so buffered successes are not
// misclassified as timed out.
func drainCheckResults(components map[string]ComponentStatus, results <-chan checkResult) {
	for {
		select {
		case r := <-results:
			components[r.name] = r.status
		default:
			return
		}
	}
}

// runChecks executes every check concurrently and collects the results keyed
// by component name. Collection is bounded by the per-check timeout plus a
// small grace margin: a check that ignores its context and blocks past the
// deadline cannot wedge the probe — it is reported as timed out and its
// goroutine is left to finish on its own (it sends into a buffered channel,
// so it never leaks on the channel). When the collection deadline fires,
// any results already in the channel are drained before force-marking the
// remainder — this avoids misclassifying buffered successes as timed out.
func (c *Checker) runChecks(ctx context.Context, checks []namedCheck) map[string]ComponentStatus {
	results := make(chan checkResult, len(checks))

	for _, nc := range checks {
		go func(nc namedCheck) {
			results <- checkResult{name: nc.name, status: c.runOne(ctx, nc)}
		}(nc)
	}

	deadline := time.NewTimer(c.cfg.checkTimeout + collectGrace)
	defer deadline.Stop()

	components := make(map[string]ComponentStatus, len(checks))
	for range checks {
		select {
		case r := <-results:
			components[r.name] = r.status
		case <-deadline.C:
			drainCheckResults(components, results)
			c.fillTimedOut(components, checks)
			return components
		}
	}
	return components
}

// fillTimedOut records a timed-out [ComponentStatus] for every check that has
// not yet reported. Used when collection hits its deadline because a check
// ignored its context.
func (c *Checker) fillTimedOut(components map[string]ComponentStatus, checks []namedCheck) {
	for _, nc := range checks {
		if _, ok := components[nc.name]; ok {
			continue
		}
		components[nc.name] = ComponentStatus{
			Status:   StatusDown,
			Error:    errTimeout(nc.name).Error(),
			Duration: c.cfg.checkTimeout.String(),
		}
	}
}

// runOne executes a single component check under a per-check timeout and
// panic recovery, mapping the outcome to a [ComponentStatus]. A check that
// exceeds the deadline is reported via [ErrTimeout]; any other failure or a
// recovered panic is reported via [ErrUnhealthy].
func (c *Checker) runOne(ctx context.Context, nc namedCheck) ComponentStatus {
	start := time.Now()
	checkCtx, cancel := context.WithTimeout(ctx, c.cfg.checkTimeout)
	defer cancel()

	err := panix.SafeVoid(opCheck, func() error {
		return nc.check(checkCtx)
	})

	cs := ComponentStatus{Status: StatusUp, Duration: time.Since(start).String()}
	if err == nil {
		return cs
	}

	cs.Status = StatusDown
	// Only a check that overran its own per-check deadline is a timeout. A
	// cancelled parent context (e.g. the client dropped the probe request)
	// is a generic failure, not a timeout of this component.
	if errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		cs.Error = errTimeout(nc.name).Error()
	} else {
		cs.Error = errUnhealthy(nc.name, err).Error()
	}
	return cs
}

// Stats returns a point-in-time snapshot of the checker counters.
func (c *Checker) Stats() CheckerStats {
	c.mu.RLock()
	registered := len(c.checks)
	c.mu.RUnlock()
	return CheckerStats{
		Registered:        registered,
		Down:              c.down.Load(),
		ReadinessChecks:   c.readinessChecks.Load(),
		ReadinessFailures: c.readinessFailures.Load(),
	}
}

// ResetStats zeroes the readiness counters. It does not affect registered
// checks or the manual up/down flag.
func (c *Checker) ResetStats() {
	c.readinessChecks.Store(0)
	c.readinessFailures.Store(0)
}
