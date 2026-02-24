// Package healthx provides a health-check registry for Kubernetes-style
// liveness and readiness probes.
//
// [Checker] aggregates named component checks and exposes them via
// [Checker.Liveness], [Checker.Readiness], and HTTP handlers.
//
//	hc := healthx.New(healthx.WithTimeout(3 * time.Second))
//	hc.Register("postgres", func(ctx context.Context) error {
//	    return db.PingContext(ctx)
//	})
//	http.Handle("/healthz", hc.LiveHandler())
//	http.Handle("/readyz", hc.ReadyHandler())
//
package healthx

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/panix"
)

// --- Status ---

// Status represents the health state of a component or the system.
type Status string

const (
	// StatusUp indicates the component or system is healthy.
	StatusUp Status = "up"

	// StatusDown indicates the component or system is unhealthy.
	StatusDown Status = "down"
)

// --- Report ---

// ComponentStatus holds the result of a single component check.
type ComponentStatus struct {
	Status   Status `json:"status"`
	Error    string `json:"error,omitempty"`
	Duration string `json:"duration"`
}

// Report is the aggregate health-check result.
type Report struct {
	Status     Status                     `json:"status"`
	Components map[string]ComponentStatus `json:"components,omitempty"`
	Duration   string                     `json:"duration"`
}

// --- Config ---

// defaultCheckTimeout is the per-check timeout used when none is specified.
const defaultCheckTimeout = 5 * time.Second

// config holds health checker parameters.
type config struct {
	checkTimeout time.Duration
}

// defaultConfig returns sensible health check defaults (5 s timeout).
func defaultConfig() config {
	return config{checkTimeout: defaultCheckTimeout}
}

// Option configures [New].
type Option func(*config)

// WithTimeout sets the per-check timeout. Values <= 0 are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.checkTimeout = d
		}
	}
}

// --- Checker ---

// namedCheck pairs a component name with its health-check function.
type namedCheck struct {
	name  string
	check func(ctx context.Context) error
}

// Checker is a health-check registry. It is safe for concurrent use.
type Checker struct {
	cfg    config
	mu     sync.RWMutex
	checks []namedCheck
	down   atomic.Bool
}

// New creates a [Checker] with the given options.
func New(opts ...Option) *Checker {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Checker{cfg: cfg}
}

// Register adds a named health check. Thread-safe. Panics if check is nil.
func (c *Checker) Register(name string, check func(ctx context.Context) error) {
	if check == nil {
		panic("healthx: Register check must not be nil for " + name)
	}
	c.mu.Lock()
	c.checks = append(c.checks, namedCheck{name: name, check: check})
	c.mu.Unlock()
}

// MarkDown manually marks the system as down (e.g. during graceful shutdown).
func (c *Checker) MarkDown() { c.down.Store(true) }

// MarkUp reverses [Checker.MarkDown].
func (c *Checker) MarkUp() { c.down.Store(false) }

// IsDown reports whether the system has been manually marked down.
func (c *Checker) IsDown() bool { return c.down.Load() }

// Liveness returns a [Report] reflecting the manual up/down state.
// It does NOT run component checks — liveness should be cheap.
func (c *Checker) Liveness(_ context.Context) Report {
	status := StatusUp
	if c.down.Load() {
		status = StatusDown
	}
	return Report{Status: status, Duration: "0s"}
}

// Readiness runs all registered checks concurrently and returns an
// aggregate [Report]. Each check runs with the configured timeout.
// A nil ctx is treated as [context.Background].
func (c *Checker) Readiness(ctx context.Context) Report {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()

	if c.down.Load() {
		return Report{
			Status:   StatusDown,
			Duration: time.Since(start).String(),
		}
	}

	c.mu.RLock()
	checks := make([]namedCheck, len(c.checks))
	copy(checks, c.checks)
	c.mu.RUnlock()

	if len(checks) == 0 {
		return Report{
			Status:   StatusUp,
			Duration: time.Since(start).String(),
		}
	}

	type result struct {
		name   string
		status ComponentStatus
	}

	results := make(chan result, len(checks))
	for _, nc := range checks {
		go func(nc namedCheck) {
			cStart := time.Now()
			checkCtx, cancel := context.WithTimeout(ctx, c.cfg.checkTimeout)
			defer cancel()

			_, err := panix.Safe[struct{}](checkCtx, "healthx."+nc.name, func(ctx context.Context) (struct{}, error) {
				return struct{}{}, nc.check(ctx)
			})
			cs := ComponentStatus{
				Status:   StatusUp,
				Duration: time.Since(cStart).String(),
			}
			if err != nil {
				cs.Status = StatusDown
				cs.Error = err.Error()
			}
			results <- result{name: nc.name, status: cs}
		}(nc)
	}

	components := make(map[string]ComponentStatus, len(checks))
	overall := StatusUp
	for range checks {
		r := <-results
		components[r.name] = r.status
		if r.status.Status == StatusDown {
			overall = StatusDown
		}
	}

	return Report{
		Status:     overall,
		Components: components,
		Duration:   time.Since(start).String(),
	}
}

// --- HTTP handlers ---

// LiveHandler returns an [http.Handler] for the liveness probe.
// Returns 200 when up, 503 when down.
func (c *Checker) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := c.Liveness(r.Context())
		writeReport(w, report)
	})
}

// ReadyHandler returns an [http.Handler] for the readiness probe.
// Returns 200 when all checks pass, 503 otherwise.
func (c *Checker) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		report := c.Readiness(r.Context())
		writeReport(w, report)
	})
}

// RegisterHandlers registers the standard Kubernetes probe endpoints:
// /healthz and /livez for liveness, /readyz for readiness.
// Panics if mux is nil.
func (c *Checker) RegisterHandlers(mux *http.ServeMux) {
	if mux == nil {
		panic("healthx: RegisterHandlers mux must not be nil")
	}
	live := c.LiveHandler()
	mux.Handle("/healthz", live)
	mux.Handle("/livez", live)
	mux.Handle("/readyz", c.ReadyHandler())
}

// writeReport serialises report as JSON and sets the HTTP status code.
func writeReport(w http.ResponseWriter, report Report) {
	w.Header().Set("Content-Type", "application/json")
	code := http.StatusOK
	if report.Status == StatusDown {
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(report)
}
