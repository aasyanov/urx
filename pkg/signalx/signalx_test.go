package signalx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

func TestContext_CancelFunc(t *testing.T) {
	ctx, cancel := Context(context.Background())
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context not cancelled")
	}
}

func TestContext_ParentCancel(t *testing.T) {
	parent, parentCancel := context.WithCancel(context.Background())
	ctx, cancel := Context(parent)
	defer cancel()

	parentCancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context not cancelled after parent cancel")
	}
}

func TestWait_RunsHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var order []int
	err := Wait(ctx, 5*time.Second,
		func(ctx context.Context) { order = append(order, 1) },
		func(ctx context.Context) { order = append(order, 2) },
		func(ctx context.Context) { order = append(order, 3) },
	)
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("expected [1,2,3], got %v", order)
	}
}

func TestWait_Timeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Wait(ctx, 50*time.Millisecond,
		func(ctx context.Context) {
			time.Sleep(200 * time.Millisecond)
		},
		func(ctx context.Context) {},
	)
	if err != context.DeadlineExceeded {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestOnShutdown_GlobalHooks(t *testing.T) {
	ResetHooks()
	defer ResetHooks()

	var called atomic.Bool
	OnShutdown(func(ctx context.Context) {
		called.Store(true)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	Wait(ctx, 5*time.Second)
	if !called.Load() {
		t.Fatal("global hook not called")
	}
}

func TestOnShutdown_GlobalBeforeLocal(t *testing.T) {
	ResetHooks()
	defer ResetHooks()

	var order []string
	OnShutdown(func(ctx context.Context) {
		order = append(order, "global")
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	Wait(ctx, 5*time.Second,
		func(ctx context.Context) { order = append(order, "local") },
	)
	if len(order) != 2 || order[0] != "global" || order[1] != "local" {
		t.Fatalf("expected [global, local], got %v", order)
	}
}

func TestResetHooks(t *testing.T) {
	ResetHooks()
	OnShutdown(func(ctx context.Context) {})
	ResetHooks()

	globalMu.Lock()
	n := len(globalHooks)
	globalMu.Unlock()
	if n != 0 {
		t.Fatalf("expected 0 hooks after reset, got %d", n)
	}
}

func TestWait_HookReceivesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var hookCtxOK bool
	Wait(ctx, 5*time.Second, func(ctx context.Context) {
		hookCtxOK = ctx.Err() == nil
	})
	if !hookCtxOK {
		t.Fatal("hook context should not be cancelled yet")
	}
}

func TestWait_HookPanic_PropagatesError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Wait(ctx, 5*time.Second,
		func(ctx context.Context) { panic("boom") },
	)
	if err == nil {
		t.Fatal("expected error from panicking hook")
	}
	var pe *errx.Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected *errx.Error, got %T: %v", err, err)
	}
}

func TestWait_HookPanic_ContinuesOtherHooks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var secondRan bool
	err := Wait(ctx, 5*time.Second,
		func(ctx context.Context) { panic("first hook panic") },
		func(ctx context.Context) { secondRan = true },
	)
	if !secondRan {
		t.Fatal("second hook should still run after first panics")
	}
	if err == nil {
		t.Fatal("expected error from panicking hook")
	}
}

func TestContext_NilParent(t *testing.T) {
	ctx, cancel := Context(nil)
	defer cancel()
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("context not cancelled")
	}
}
