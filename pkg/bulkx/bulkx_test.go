package bulkx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- New: defaults ---

func TestNew_Defaults(t *testing.T) {
	bh := New()
	if bh.Active() != 0 {
		t.Fatalf("expected 0 active, got %d", bh.Active())
	}
	if bh.IsClosed() {
		t.Fatal("expected not closed")
	}
}

// --- New: with options ---

func TestNew_WithOptions(t *testing.T) {
	bh := New(WithMaxConcurrent(3), WithTimeout(50*time.Millisecond))
	if bh.cfg.maxConcurrent != 3 {
		t.Fatalf("expected maxConcurrent=3, got %d", bh.cfg.maxConcurrent)
	}
	if bh.cfg.timeout != 50*time.Millisecond {
		t.Fatalf("expected timeout=50ms, got %v", bh.cfg.timeout)
	}
}

// --- New: invalid options ignored ---

func TestNew_InvalidOptions(t *testing.T) {
	bh := New(WithMaxConcurrent(-1), WithTimeout(-1))
	if bh.cfg.maxConcurrent != 10 {
		t.Fatalf("expected maxConcurrent=10 (negative ignored), got %d", bh.cfg.maxConcurrent)
	}
	if bh.cfg.timeout != 30*time.Second {
		t.Fatalf("expected default timeout, got %v", bh.cfg.timeout)
	}
}

// --- Execute: success ---

func TestExecute_Success(t *testing.T) {
	bh := New()
	_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- Execute: propagates fn error ---

func TestExecute_PropagatesError(t *testing.T) {
	bh := New()
	want := errors.New("boom")
	_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}

// --- Execute: limits concurrency ---

func TestExecute_LimitsConcurrency(t *testing.T) {
	const max = 3
	bh := New(WithMaxConcurrent(max))

	var peak atomic.Int32
	var wg sync.WaitGroup

	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
				cur := bh.Active()
				for {
					old := peak.Load()
					if int32(cur) <= old || peak.CompareAndSwap(old, int32(cur)) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				return struct{}{}, nil
			})
		}()
	}
	wg.Wait()

	if peak.Load() > max {
		t.Fatalf("peak concurrency %d exceeded max %d", peak.Load(), max)
	}
}

// --- Execute: timeout ---

func TestExecute_Timeout(t *testing.T) {
	bh := New(WithMaxConcurrent(1), WithTimeout(30*time.Millisecond))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	close(blocker)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainBulk || xe.Code != CodeTimeout {
		t.Fatalf("expected BULK/TIMEOUT, got %s/%s", xe.Domain, xe.Code)
	}
}

// --- Execute: pre-cancelled context (fast reject) ---

func TestExecute_PreCancelledContext(t *testing.T) {
	bh := New(WithMaxConcurrent(10))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Execute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	s := bh.Stats()
	if s.Rejected != 1 {
		t.Fatalf("expected Rejected=1, got %d", s.Rejected)
	}
}

// --- Execute: optimistic path (slot available, no timer) ---

func TestExecute_OptimisticPath(t *testing.T) {
	bh := New(WithMaxConcurrent(10), WithTimeout(50*time.Millisecond))
	var count atomic.Int32

	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
				count.Add(1)
				return struct{}{}, nil
			})
			if err != nil {
				t.Errorf("expected nil, got %v", err)
			}
		}()
	}
	wg.Wait()

	if count.Load() != 5 {
		t.Fatalf("expected 5 executions, got %d", count.Load())
	}
}

// --- Execute: slow path (slot acquired while timer is running) ---

func TestExecute_SlowPathSlotAcquired(t *testing.T) {
	bh := New(WithMaxConcurrent(1), WithTimeout(5*time.Second))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
			return struct{}{}, nil
		})
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	close(blocker)

	err := <-done
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- Execute: context cancellation (slow path) ---

func TestExecute_ContextCancelled(t *testing.T) {
	bh := New(WithMaxConcurrent(1))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := Execute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	close(blocker)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// --- Execute: closed ---

func TestExecute_Closed(t *testing.T) {
	bh := New()
	bh.Close()

	_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainBulk || xe.Code != CodeClosed {
		t.Fatalf("expected BULK/CLOSED, got %s/%s", xe.Domain, xe.Code)
	}
}

// --- Execute: panic recovery ---

func TestExecute_PanicRecovery(t *testing.T) {
	bh := New()
	_, err := Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error from panic, got %T", err)
	}
}

// --- TryExecute: success ---

func TestTryExecute_Success(t *testing.T) {
	bh := New()
	ok, _, err := TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// --- TryExecute: no slot available ---

func TestTryExecute_NoSlot(t *testing.T) {
	bh := New(WithMaxConcurrent(1))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	ok, _, err := TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	close(blocker)

	if ok {
		t.Fatal("expected ok=false when no slot")
	}
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

// --- TryExecute: closed ---

func TestTryExecute_Closed(t *testing.T) {
	bh := New()
	bh.Close()

	ok, _, err := TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	if ok {
		t.Fatal("expected ok=false when closed")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED, got %s", xe.Code)
	}
}

// --- TryExecute: panic recovery ---

func TestTryExecute_PanicRecovery(t *testing.T) {
	bh := New()
	ok, _, err := TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		panic("boom")
	})
	if !ok {
		t.Fatal("expected ok=true (slot was acquired)")
	}
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
}

// --- BulkController ---

func TestBulkController_Active(t *testing.T) {
	bh := New(WithMaxConcurrent(5))
	started := make(chan struct{})
	blocker := make(chan struct{})

	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		close(started)
		<-blocker
		return struct{}{}, nil
	})
	<-started

	var seen int
	_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		seen = bc.Active()
		return struct{}{}, nil
	})
	close(blocker)

	if seen < 1 {
		t.Fatalf("expected Active >= 1, got %d", seen)
	}
}

func TestBulkController_MaxConcurrent(t *testing.T) {
	bh := New(WithMaxConcurrent(7))
	var seen int
	Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		seen = bc.MaxConcurrent()
		return struct{}{}, nil
	})
	if seen != 7 {
		t.Fatalf("expected MaxConcurrent=7, got %d", seen)
	}
}

func TestBulkController_WaitedSlot_Optimistic(t *testing.T) {
	bh := New(WithMaxConcurrent(10))
	var waited bool
	Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		waited = bc.WaitedSlot()
		return struct{}{}, nil
	})
	if waited {
		t.Fatal("optimistic path should not have WaitedSlot=true")
	}
}

func TestBulkController_WaitedSlot_SlowPath(t *testing.T) {
	bh := New(WithMaxConcurrent(1), WithTimeout(5*time.Second))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	var waited bool
	done := make(chan struct{})
	go func() {
		Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
			waited = bc.WaitedSlot()
			return struct{}{}, nil
		})
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	close(blocker)
	<-done

	if !waited {
		t.Fatal("slow path should have WaitedSlot=true")
	}
}

func TestBulkController_TryExecute(t *testing.T) {
	bh := New(WithMaxConcurrent(10))
	var waited bool
	TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		waited = bc.WaitedSlot()
		return struct{}{}, nil
	})
	if waited {
		t.Fatal("TryExecute should never have WaitedSlot=true")
	}
}

// --- Active: tracks in-flight ---

func TestActive_TracksInFlight(t *testing.T) {
	bh := New(WithMaxConcurrent(5))

	started := make(chan struct{})
	blocker := make(chan struct{})

	for range 3 {
		go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
			started <- struct{}{}
			<-blocker
			return struct{}{}, nil
		})
	}
	for range 3 {
		<-started
	}

	if bh.Active() != 3 {
		t.Fatalf("expected 3 active, got %d", bh.Active())
	}
	close(blocker)
	time.Sleep(10 * time.Millisecond)
	if bh.Active() != 0 {
		t.Fatalf("expected 0 active after completion, got %d", bh.Active())
	}
}

// --- Close: idempotent ---

func TestClose_Idempotent(t *testing.T) {
	bh := New()
	bh.Close()
	bh.Close() // must not panic
	if !bh.IsClosed() {
		t.Fatal("expected closed")
	}
}

// --- Domain/Code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainBulk != "BULK" {
		t.Fatalf("expected BULK, got %s", DomainBulk)
	}
}

func TestCodeConstants(t *testing.T) {
	codes := map[string]string{
		"CodeTimeout": CodeTimeout,
		"CodeClosed":  CodeClosed,
	}
	want := map[string]string{
		"CodeTimeout": "TIMEOUT",
		"CodeClosed":  "CLOSED",
	}
	for name, got := range codes {
		if got != want[name] {
			t.Errorf("%s = %q, want %q", name, got, want[name])
		}
	}
}

// --- Error constructors ---

func TestErrTimeout(t *testing.T) {
	e := errTimeout()
	if e.Domain != DomainBulk || e.Code != CodeTimeout {
		t.Fatalf("expected BULK/TIMEOUT, got %s/%s", e.Domain, e.Code)
	}
}

func TestErrClosed(t *testing.T) {
	e := errClosed()
	if e.Domain != DomainBulk || e.Code != CodeClosed {
		t.Fatalf("expected BULK/CLOSED, got %s/%s", e.Domain, e.Code)
	}
}

// --- Stats ---

func TestStats_Initial(t *testing.T) {
	bh := New(WithMaxConcurrent(5))
	s := bh.Stats()
	if s.MaxConcurrent != 5 {
		t.Fatalf("expected MaxConcurrent=5, got %d", s.MaxConcurrent)
	}
	if s.Active != 0 || s.Executed != 0 || s.Rejected != 0 || s.Timeouts != 0 {
		t.Fatalf("expected all zeros, got %+v", s)
	}
}

func TestStats_AfterExecute(t *testing.T) {
	bh := New(WithMaxConcurrent(5))
	_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	s := bh.Stats()
	if s.Executed != 2 {
		t.Fatalf("expected Executed=2, got %d", s.Executed)
	}
}

func TestStats_AfterTimeout(t *testing.T) {
	bh := New(WithMaxConcurrent(1), WithTimeout(10*time.Millisecond))

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
			close(started)
			<-release
			return struct{}{}, nil
		})
	}()
	<-started

	_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	close(release)

	time.Sleep(20 * time.Millisecond)
	s := bh.Stats()
	if s.Timeouts != 1 {
		t.Fatalf("expected Timeouts=1, got %d", s.Timeouts)
	}
}

func TestStats_TryExecuteRejected(t *testing.T) {
	bh := New(WithMaxConcurrent(1))

	started := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
			close(started)
			<-release
			return struct{}{}, nil
		})
	}()
	<-started

	ok, _, _ := TryExecute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	close(release)

	if ok {
		t.Fatal("expected TryExecute to fail")
	}

	time.Sleep(20 * time.Millisecond)
	s := bh.Stats()
	if s.Rejected != 1 {
		t.Fatalf("expected Rejected=1, got %d", s.Rejected)
	}
}

func TestResetStats(t *testing.T) {
	bh := New()
	_, _ = Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		return struct{}{}, nil
	})
	bh.ResetStats()
	s := bh.Stats()
	if s.Executed != 0 || s.Rejected != 0 || s.Timeouts != 0 {
		t.Fatalf("expected all zeros after reset, got %+v", s)
	}
}

func TestExecute_ContextCancelled_ReturnsErrx(t *testing.T) {
	bh := New(WithMaxConcurrent(1))

	blocker := make(chan struct{})
	go Execute[struct{}](bh, context.Background(), func(ctx context.Context, bc BulkController) (struct{}, error) {
		<-blocker
		return struct{}{}, nil
	})
	time.Sleep(5 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := Execute[struct{}](bh, ctx, func(ctx context.Context, bc BulkController) (struct{}, error) {
		t.Fatal("should not run")
		return struct{}{}, nil
	})
	close(blocker)

	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T: %v", err, err)
	}
	if xe.Domain != DomainBulk || xe.Code != CodeCancelled {
		t.Fatalf("expected BULK/CANCELLED, got %s/%s", xe.Domain, xe.Code)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded in chain, got %v", err)
	}
}

func TestCodeConstant_Cancelled(t *testing.T) {
	if CodeCancelled != "CANCELLED" {
		t.Fatalf("expected CANCELLED, got %s", CodeCancelled)
	}
}

func TestErrCancelled(t *testing.T) {
	e := errCancelled(context.Canceled)
	if e.Domain != DomainBulk || e.Code != CodeCancelled {
		t.Fatalf("expected BULK/CANCELLED, got %s/%s", e.Domain, e.Code)
	}
	if !errors.Is(e, context.Canceled) {
		t.Fatal("expected context.Canceled in chain")
	}
}
