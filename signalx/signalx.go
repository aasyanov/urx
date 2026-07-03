// Package signalx provides OS signal trapping and graceful shutdown for Go
// services.
//
// [Trap] derives a context that is cancelled when the process receives one
// of the configured OS signals (default: SIGINT, SIGTERM). [Wait] blocks
// until that context is done, then runs shutdown hooks in registration
// order under a bounded timeout. [OnShutdown] registers process-global
// hooks that run before any hooks passed directly to [Wait].
//
// # Quick Start
//
//	ctx, cancel := signalx.Trap(context.Background())
//	defer cancel()
//
//	// start servers using ctx ...
//
//	err := signalx.Wait(ctx,
//	    func(ctx context.Context) { server.Shutdown(ctx) },
//	    func(ctx context.Context) { db.Close() },
//	)
//
// # Zero Dependencies
//
// signalx depends only on the Go standard library and the urx panix package
// for panic-safe hook execution.
package signalx

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"time"

	"github.com/aasyanov/urx/panix"
)

// opWait labels panics recovered while running a shutdown hook.
const opWait = "signalx.Wait"

// globalHooks is the process-global shutdown hook registry, protected by
// globalMu. Hooks run in registration order when [Wait] is called.
var (
	globalMu    sync.Mutex
	globalHooks []func(ctx context.Context)
)

// OnShutdown registers a process-global shutdown hook. Hooks run in
// registration order on every [Wait] call, before any hooks passed directly
// to that call. Register hooks once during process startup; do not rely on
// them running exactly once if Wait is invoked concurrently. It is safe for
// concurrent registration.
//
// Panics if fn is nil.
func OnShutdown(fn func(ctx context.Context)) {
	if fn == nil {
		panic("signalx: OnShutdown hook must not be nil")
	}
	globalMu.Lock()
	globalHooks = append(globalHooks, fn)
	globalMu.Unlock()
}

// ResetHooks removes all process-global shutdown hooks registered with
// [OnShutdown]. It is intended for tests that need a clean registry between
// cases. It is safe for concurrent use.
func ResetHooks() {
	globalMu.Lock()
	globalHooks = nil
	globalMu.Unlock()
}

// Trap returns a context derived from parent that is cancelled when the
// process receives one of the given signals. When no signals are supplied,
// it traps SIGINT and SIGTERM. A nil parent is treated as
// [context.Background].
//
// Panics if any explicit signal argument is nil.
//
// The returned [context.CancelFunc] must be called to release the signal
// watcher goroutine and stop signal delivery, even if a signal arrives
// first. It is safe for concurrent use and idempotent.
func Trap(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if len(signals) == 0 {
		signals = defaultSignals
	} else {
		for _, sig := range signals {
			if sig == nil {
				panic("signalx: Trap signal must not be nil")
			}
		}
	}

	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, signals...)

	go func() {
		defer signal.Stop(ch)
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
	}()

	return ctx, cancel
}

// Wait blocks until ctx is done, then runs every process-global hook
// registered via [OnShutdown] followed by the provided hooks, all in
// registration order. Each hook is invoked with a context carrying the
// configured shutdown timeout (default 15s, see [WithTimeout]) and is run
// under panic recovery so a panicking hook cannot abort the rest.
//
// Call Wait once per shutdown sequence (typically from a single goroutine
// after [Trap]). Concurrent Wait calls each run the full global hook list
// independently.
//
// Wait returns nil when all hooks complete cleanly. It returns
// [ErrShutdownTimeout] if the timeout elapses before every hook finishes,
// and [ErrHookPanic] (joined with the recovered causes) if any hook panics.
// Both sentinels may appear together when a hook both panics and the drain
// budget is exhausted.
//
// Panics if any per-call hook argument is nil.
func Wait(ctx context.Context, hooks ...func(ctx context.Context)) error {
	return WaitWith(ctx, nil, hooks...)
}

// WaitWith behaves like [Wait] but accepts functional [Option] values to
// override the shutdown timeout and related behavior. It is the configurable
// form of [Wait]; prefer [Wait] when the defaults suffice.
//
// Panics if any hook in the combined global + per-call list is nil.
func WaitWith(ctx context.Context, opts []Option, hooks ...func(ctx context.Context)) error {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := newConfig(opts)

	<-ctx.Done()

	shutCtx, cancel := shutdownContext(cfg.timeout)
	defer cancel()

	all := collectHooks(hooks)
	assertHooks(all)

	var errs []error
	var timedOut bool
	for _, hook := range all {
		if shutCtx.Err() != nil {
			timedOut = true
			break
		}
		if err := runHook(shutCtx, hook); err != nil {
			errs = append(errs, err)
		}
	}
	// A hook may overrun the deadline without there being a subsequent hook to
	// observe it at the top of the loop, so check once more after the loop.
	if !timedOut && cfg.timeout > 0 && shutCtx.Err() != nil {
		timedOut = true
	}
	if timedOut {
		errs = append(errs, ErrShutdownTimeout)
	}
	return errors.Join(errs...)
}

// shutdownContext returns a context bounding hook execution. A non-positive
// timeout yields a plain cancellable context with no deadline.
func shutdownContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}
	return context.WithTimeout(context.Background(), timeout)
}

// assertHooks panics when any hook in the snapshot is nil. Nil hooks are
// programmer errors caught before execution, consistent with [OnShutdown].
func assertHooks(hooks []func(ctx context.Context)) {
	for i, hook := range hooks {
		if hook == nil {
			panic("signalx: shutdown hook at index " + strconv.Itoa(i) + " must not be nil")
		}
	}
}

// collectHooks snapshots the global hooks under lock and appends the
// per-call hooks, preserving registration order.
func collectHooks(hooks []func(ctx context.Context)) []func(ctx context.Context) {
	globalMu.Lock()
	defer globalMu.Unlock()

	all := make([]func(ctx context.Context), 0, len(globalHooks)+len(hooks))
	all = append(all, globalHooks...)
	all = append(all, hooks...)
	return all
}

// runHook executes a single hook under panic recovery, mapping any recovered
// panic to [ErrHookPanic] joined with the underlying [*panix.PanicError].
func runHook(ctx context.Context, hook func(ctx context.Context)) error {
	err := panix.SafeVoid(opWait, func() error {
		hook(ctx)
		return nil
	})
	if err != nil {
		return errors.Join(ErrHookPanic, err)
	}
	return nil
}
