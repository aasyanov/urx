// Package signalx provides graceful shutdown primitives for Go services.
//
// [Context] returns a context that is cancelled when the process receives
// one of the specified OS signals (default: SIGINT, SIGTERM). [Wait] blocks
// until the context is done, then runs shutdown hooks in order with a
// timeout. [OnShutdown] registers global hooks.
//
//	ctx, cancel := signalx.Context(context.Background())
//	defer cancel()
//
//	// start servers using ctx ...
//
//	signalx.Wait(ctx, 10*time.Second,
//	    func(ctx context.Context) { server.Shutdown(ctx) },
//	    func(ctx context.Context) { db.Close() },
//	)
package signalx

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

// --- Global hooks ---

// Global shutdown hook registry, protected by globalMu.
var (
	globalMu    sync.Mutex
	globalHooks []func(ctx context.Context)
)

// OnShutdown registers a global shutdown hook. Hooks run in registration
// order when [Wait] is called. Thread-safe.
func OnShutdown(fn func(ctx context.Context)) {
	globalMu.Lock()
	globalHooks = append(globalHooks, fn)
	globalMu.Unlock()
}

// ResetHooks clears all global shutdown hooks. Intended for testing.
func ResetHooks() {
	globalMu.Lock()
	globalHooks = nil
	globalMu.Unlock()
}

// --- Context ---

// Context returns a context derived from parent that is cancelled when the
// process receives one of the given signals. If no signals are specified,
// defaults to SIGINT and SIGTERM. A nil parent is treated as [context.Background].
func Context(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if len(signals) == 0 {
		signals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	}
	ctx, cancel := context.WithCancel(parent)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, signals...)
	go func() {
		select {
		case <-ch:
			cancel()
		case <-ctx.Done():
		}
		signal.Stop(ch)
	}()
	return ctx, cancel
}

// --- Wait ---

// Wait blocks until ctx is done, then runs all global hooks followed by
// the provided hooks in order. Each hook receives a context with the given
// shutdownTimeout. Returns context.DeadlineExceeded if hooks don't
// complete in time, or an [errx.MultiError] if any hooks panic.
func Wait(ctx context.Context, shutdownTimeout time.Duration, hooks ...func(ctx context.Context)) error {
	<-ctx.Done()

	shutCtx, shutCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutCancel()

	globalMu.Lock()
	all := make([]func(ctx context.Context), 0, len(globalHooks)+len(hooks))
	all = append(all, globalHooks...)
	all = append(all, hooks...)
	globalMu.Unlock()

	me := errx.NewMulti()
	for _, hook := range all {
		if shutCtx.Err() != nil {
			return shutCtx.Err()
		}
		h := hook
		_, err := panix.Safe[struct{}](shutCtx, "signalx.Wait", func(ctx context.Context) (struct{}, error) {
			h(ctx)
			return struct{}{}, nil
		})
		if err != nil {
			me.Add(err)
		}
	}
	return me.Err()
}
