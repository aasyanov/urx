// Package cronx provides a minimal, reliable job scheduler for Go services.
//
// A [Scheduler] runs jobs at a fixed [time.Duration] interval. One-off jobs
// (interval <= 0) execute once at [Scheduler.Start]. Each job receives a
// [JobController] that exposes execution state and lets the function influence
// its own schedule — abort permanently, change interval, or suppress error
// recording.
//
//	s := cronx.New()
//	cronx.AddJob(s, "cleanup", 5*time.Minute, func(ctx context.Context, jc cronx.JobController) (int, error) {
//	    if jc.RunNumber() > 100 {
//	        jc.Reschedule(30 * time.Minute)
//	    }
//	    return cleanup(ctx)
//	})
//	s.Start(ctx)
//	defer s.Stop(30 * time.Second)
//
// Every job execution is wrapped with [panix.Safe] for panic recovery and
// [ctxx.WithSpan] for per-run trace spans. The scheduler does not retry,
// log, rate-limit, or distribute — those are separate URX concerns.
package cronx

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

// --- Configuration ---

type config struct {
	name             string
	failureThreshold float64
}

func defaultConfig() config {
	return config{
		name:             "cronx",
		failureThreshold: 0.30,
	}
}

// Option configures [New] behavior.
type Option func(*config)

// WithName sets the scheduler name used in [panix.Safe] operation labels.
func WithName(name string) Option {
	return func(c *config) {
		if name != "" {
			c.name = name
		}
	}
}

// WithFailureThreshold sets the failure-rate threshold for [Scheduler.HealthCheck].
// Values outside (0, 1] are ignored. Default: 0.30.
func WithFailureThreshold(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.failureThreshold = f
		}
	}
}

// --- JobController ---

// JobController provides execution context to the job function.
// The implementation is private; callers interact only through this interface.
type JobController interface {
	// RunNumber returns the current run count (1-based).
	RunNumber() int64
	// LastRunTime returns the start time of the previous run (zero if first).
	LastRunTime() time.Time
	// Abort stops this job permanently. No more runs will be scheduled.
	Abort()
	// Reschedule changes the job interval starting from the next run.
	// Values <= 0 are ignored.
	Reschedule(d time.Duration)
	// SkipError tells the scheduler not to count the current error as a failure.
	SkipError()
}

type jobExecution struct {
	j       *job
	skipErr atomic.Bool
}

// RunNumber implements [JobController].
func (e *jobExecution) RunNumber() int64 { return e.j.runCount.Load() }

// LastRunTime implements [JobController].
func (e *jobExecution) LastRunTime() time.Time { return e.j.loadPrevRun() }

// Abort implements [JobController].
func (e *jobExecution) Abort() { e.j.abort() }

// Reschedule implements [JobController].
func (e *jobExecution) Reschedule(d time.Duration) {
	if d > 0 {
		e.j.interval.Store(d.Nanoseconds())
		e.j.signalWake()
	}
}

// SkipError implements [JobController].
func (e *jobExecution) SkipError() { e.skipErr.Store(true) }

// --- Stats ---

// JobStats holds per-job runtime statistics.
type JobStats struct {
	Name        string        `json:"name"`
	Interval    time.Duration `json:"interval"`
	TotalRuns   int64         `json:"total_runs"`
	SuccessRuns int64         `json:"success_runs"`
	FailureRuns int64         `json:"failure_runs"`
	LastRunTime time.Time     `json:"last_run_time"`
	LastDuration time.Duration `json:"last_duration"`
}

// Stats holds scheduler-wide statistics.
type Stats struct {
	Name        string     `json:"name"`
	TotalJobs   int        `json:"total_jobs"`
	ActiveJobs  int        `json:"active_jobs"`
	TotalRuns   int64      `json:"total_runs"`
	SuccessRuns int64      `json:"success_runs"`
	FailureRuns int64      `json:"failure_runs"`
	Jobs        []JobStats `json:"jobs,omitempty"`
}

// --- Scheduler ---

// Scheduler manages periodic jobs with graceful shutdown. Create one with
// [New], register jobs with [AddJob], start with [Start], stop with [Stop].
type Scheduler struct {
	cfg config

	mu      sync.Mutex
	jobs    []*job
	started bool
	stopped bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	total   atomic.Int64
	success atomic.Int64
	failed  atomic.Int64
}

// New creates a [Scheduler] with the given options.
func New(opts ...Option) *Scheduler {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.failureThreshold <= 0 || cfg.failureThreshold > 1 {
		cfg.failureThreshold = 0.30
	}
	return &Scheduler{cfg: cfg}
}

// AddJob registers a generic job. The job runs every interval; if interval <= 0
// it runs once at [Start]. Because Go methods cannot have type parameters,
// AddJob is a package-level generic function.
func AddJob[T any](s *Scheduler, name string, interval time.Duration, fn func(context.Context, JobController) (T, error)) error {
	if s == nil {
		return errInvalidInput("scheduler must not be nil")
	}
	if fn == nil {
		return errNilFunc()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return errClosed()
	}

	if name == "" {
		name = fmt.Sprintf("job-%d", len(s.jobs)+1)
	}

	j := &job{
		name: name,
		done: make(chan struct{}),
		wake: make(chan struct{}, 1),
	}
	j.interval.Store(interval.Nanoseconds())
	j.fn = func(ctx context.Context, jc JobController) (any, error) {
		return fn(ctx, jc)
	}

	s.jobs = append(s.jobs, j)

	if s.started {
		s.wg.Add(1)
		go s.runLoop(s.ctx, j)
	}
	return nil
}

// Start starts all registered jobs. Returns [CodeAlreadyStarted] on double
// start, [CodeClosed] after stop.
func (s *Scheduler) Start(parent context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.stopped {
		return errClosed()
	}
	if s.started {
		return errAlreadyStarted()
	}
	if parent == nil {
		parent = context.Background()
	}
	s.ctx, s.cancel = context.WithCancel(parent)
	s.started = true

	for _, j := range s.jobs {
		s.wg.Add(1)
		go s.runLoop(s.ctx, j)
	}
	return nil
}

// Stop cancels all job loops and waits for in-flight runs up to timeout.
// Stop is idempotent after the first call.
func (s *Scheduler) Stop(timeout time.Duration) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return errNotStarted()
	}
	if s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.cancel()
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	if timeout <= 0 {
		<-done
		return nil
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return nil
	case <-t.C:
		return errShutdownTimeout(timeout)
	}
}

// IsClosed reports whether [Stop] has been called.
func (s *Scheduler) IsClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stopped
}

// Stats returns a point-in-time snapshot of scheduler statistics.
func (s *Scheduler) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := Stats{
		Name:        s.cfg.name,
		TotalJobs:   len(s.jobs),
		TotalRuns:   s.total.Load(),
		SuccessRuns: s.success.Load(),
		FailureRuns: s.failed.Load(),
	}
	for _, j := range s.jobs {
		js := j.stats()
		if js.TotalRuns > 0 {
			out.ActiveJobs++
		}
		out.Jobs = append(out.Jobs, js)
	}
	return out
}

// ResetStats zeroes all counters.
func (s *Scheduler) ResetStats() {
	s.total.Store(0)
	s.success.Store(0)
	s.failed.Store(0)

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		j.resetStats()
	}
}

// HealthCheck returns an error if the failure rate exceeds the configured
// threshold. Suitable for [healthx.Register].
func (s *Scheduler) HealthCheck(context.Context) error {
	total := s.total.Load()
	if total == 0 {
		return nil
	}
	rate := float64(s.failed.Load()) / float64(total)
	if rate > s.cfg.failureThreshold {
		return errx.New(DomainCron, CodeJobFailed, "failure rate above threshold",
			errx.WithMeta("failure_rate", fmt.Sprintf("%.4f", rate)),
			errx.WithMeta("threshold", fmt.Sprintf("%.4f", s.cfg.failureThreshold)),
		)
	}
	return nil
}

// --- job ---

type job struct {
	name     string
	interval atomic.Int64 // nanoseconds; <=0 means one-off
	fn       func(context.Context, JobController) (any, error)
	done     chan struct{}
	wake     chan struct{}
	abortOnce sync.Once

	runCount    atomic.Int64
	successRuns atomic.Int64
	failureRuns atomic.Int64
	prevRunNano atomic.Int64 // UnixNano of previous run start
	lastRunNano atomic.Int64 // UnixNano
	lastDurNano atomic.Int64 // nanoseconds
}

func (j *job) abort() {
	j.abortOnce.Do(func() { close(j.done) })
}

func (j *job) signalWake() {
	select {
	case j.wake <- struct{}{}:
	default:
	}
}

func (j *job) loadLastRun() time.Time {
	n := j.lastRunNano.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func (j *job) loadPrevRun() time.Time {
	n := j.prevRunNano.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

func (j *job) stats() JobStats {
	return JobStats{
		Name:         j.name,
		Interval:     time.Duration(j.interval.Load()),
		TotalRuns:    j.runCount.Load(),
		SuccessRuns:  j.successRuns.Load(),
		FailureRuns:  j.failureRuns.Load(),
		LastRunTime:  j.loadLastRun(),
		LastDuration: time.Duration(j.lastDurNano.Load()),
	}
}

func (j *job) resetStats() {
	j.runCount.Store(0)
	j.successRuns.Store(0)
	j.failureRuns.Store(0)
	j.prevRunNano.Store(0)
	j.lastRunNano.Store(0)
	j.lastDurNano.Store(0)
}

// --- run loop ---

func (s *Scheduler) runLoop(ctx context.Context, j *job) {
	defer s.wg.Done()

	ivl := time.Duration(j.interval.Load())
	if ivl <= 0 {
		s.executeJob(ctx, j)
		return
	}

	timer := time.NewTimer(ivl)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-j.done:
			return
		case <-j.wake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(time.Duration(j.interval.Load()))
		case <-timer.C:
			s.executeJob(ctx, j)
			timer.Reset(time.Duration(j.interval.Load()))
		}
	}
}

func (s *Scheduler) executeJob(ctx context.Context, j *job) {
	now := time.Now()
	if prev := j.lastRunNano.Load(); prev != 0 {
		j.prevRunNano.Store(prev)
	}
	j.runCount.Add(1)
	j.lastRunNano.Store(now.UnixNano())
	s.total.Add(1)

	runCtx := ctxx.WithSpan(ctx)
	ctrl := &jobExecution{j: j}

	start := time.Now()
	_, err := panix.Safe[any](runCtx, s.cfg.name+"."+j.name, func(inner context.Context) (any, error) {
		return j.fn(inner, ctrl)
	})
	dur := time.Since(start)
	j.lastDurNano.Store(dur.Nanoseconds())

	if err != nil && !ctrl.skipErr.Load() {
		j.failureRuns.Add(1)
		s.failed.Add(1)
	} else {
		j.successRuns.Add(1)
		s.success.Add(1)
	}
}
