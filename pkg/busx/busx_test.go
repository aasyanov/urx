package busx

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Subscribe ---

func TestSubscribe(t *testing.T) {
	b := New()
	called := false
	id, err := b.Subscribe("evt", func(ctx context.Context, event string, payload any) {
		called = true
	})
	if err != nil {
		t.Fatalf("Subscribe: unexpected error: %v", err)
	}
	if id == 0 {
		t.Fatal("Subscribe: expected non-zero ID")
	}
	if err := b.Publish(context.Background(), "evt", nil); err != nil {
		t.Fatalf("Publish: unexpected error: %v", err)
	}
	if !called {
		t.Fatal("handler was not called")
	}
}

func TestSubscribe_MultipleHandlers(t *testing.T) {
	b := New()
	var order []int
	for i := range 3 {
		v := i
		if _, err := b.Subscribe("evt", func(ctx context.Context, event string, payload any) {
			order = append(order, v)
		}); err != nil {
			t.Fatalf("Subscribe[%d]: %v", i, err)
		}
	}
	if err := b.Publish(context.Background(), "evt", nil); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(order))
	}
	for i, v := range order {
		if v != i {
			t.Fatalf("order[%d] = %d, want %d", i, v, i)
		}
	}
}

func TestSubscribe_NilHandler(t *testing.T) {
	b := New()
	_, err := b.Subscribe("evt", nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainBus {
		t.Fatalf("domain = %q, want %q", xe.Domain, DomainBus)
	}
	if xe.Code != CodeNilHandler {
		t.Fatalf("code = %q, want %q", xe.Code, CodeNilHandler)
	}
}

func TestSubscribe_OnClosedBus(t *testing.T) {
	b := New()
	b.Close()
	_, err := b.Subscribe("evt", func(context.Context, string, any) {})
	if err == nil {
		t.Fatal("expected error on closed bus")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeClosed)
	}
}

func TestSubscribe_UniqueIDs(t *testing.T) {
	b := New()
	seen := make(map[SubscriptionID]bool)
	for range 100 {
		id, err := b.Subscribe("evt", func(context.Context, string, any) {})
		if err != nil {
			t.Fatalf("Subscribe: %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate ID: %d", id)
		}
		seen[id] = true
	}
}

// --- Unsubscribe ---

func TestUnsubscribe(t *testing.T) {
	b := New()
	called := false
	id, _ := b.Subscribe("evt", func(context.Context, string, any) {
		called = true
	})
	if !b.Unsubscribe(id) {
		t.Fatal("Unsubscribe returned false for valid ID")
	}
	_ = b.Publish(context.Background(), "evt", nil)
	if called {
		t.Fatal("handler called after Unsubscribe")
	}
}

func TestUnsubscribe_NonExistentID(t *testing.T) {
	b := New()
	if b.Unsubscribe(999) {
		t.Fatal("Unsubscribe returned true for non-existent ID")
	}
}

func TestUnsubscribe_RemovesEventWhenEmpty(t *testing.T) {
	b := New()
	id, _ := b.Subscribe("evt", func(context.Context, string, any) {})
	b.Unsubscribe(id)
	events := b.Events()
	for _, e := range events {
		if e == "evt" {
			t.Fatal("event still present after last handler removed")
		}
	}
}

func TestUnsubscribe_KeepsOtherHandlers(t *testing.T) {
	b := New()
	var calls []string
	id1, _ := b.Subscribe("evt", func(context.Context, string, any) {
		calls = append(calls, "h1")
	})
	_, _ = b.Subscribe("evt", func(context.Context, string, any) {
		calls = append(calls, "h2")
	})
	b.Unsubscribe(id1)
	_ = b.Publish(context.Background(), "evt", nil)
	if len(calls) != 1 || calls[0] != "h2" {
		t.Fatalf("calls = %v, want [h2]", calls)
	}
}

func TestUnsubscribe_DoubleUnsubscribe(t *testing.T) {
	b := New()
	id, _ := b.Subscribe("evt", func(context.Context, string, any) {})
	b.Unsubscribe(id)
	if b.Unsubscribe(id) {
		t.Fatal("second Unsubscribe returned true")
	}
}

// --- Publish ---

func TestPublish_PayloadDelivery(t *testing.T) {
	b := New()
	var got any
	b.Subscribe("evt", func(_ context.Context, _ string, payload any) {
		got = payload
	})
	want := "hello"
	_ = b.Publish(context.Background(), "evt", want)
	if got != want {
		t.Fatalf("payload = %v, want %v", got, want)
	}
}

func TestPublish_EventNameDelivery(t *testing.T) {
	b := New()
	var got string
	b.Subscribe("user.created", func(_ context.Context, event string, _ any) {
		got = event
	})
	_ = b.Publish(context.Background(), "user.created", nil)
	if got != "user.created" {
		t.Fatalf("event = %q, want %q", got, "user.created")
	}
}

func TestPublish_ContextPropagation(t *testing.T) {
	b := New()
	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "val")
	var got any
	b.Subscribe("evt", func(ctx context.Context, _ string, _ any) {
		got = ctx.Value(ctxKey{})
	})
	_ = b.Publish(ctx, "evt", nil)
	if got != "val" {
		t.Fatalf("context value = %v, want %q", got, "val")
	}
}

func TestPublish_NoSubscribers(t *testing.T) {
	b := New()
	err := b.Publish(context.Background(), "nobody", nil)
	if err != nil {
		t.Fatalf("expected nil error for no subscribers, got %v", err)
	}
}

func TestPublish_OnClosedBus(t *testing.T) {
	b := New()
	b.Close()
	err := b.Publish(context.Background(), "evt", nil)
	if err == nil {
		t.Fatal("expected error on closed bus")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeClosed)
	}
}

func TestPublish_MultipleEvents(t *testing.T) {
	b := New()
	var gotA, gotB bool
	b.Subscribe("a", func(context.Context, string, any) { gotA = true })
	b.Subscribe("b", func(context.Context, string, any) { gotB = true })
	_ = b.Publish(context.Background(), "a", nil)
	if !gotA {
		t.Fatal("handler for 'a' not called")
	}
	if gotB {
		t.Fatal("handler for 'b' should not be called")
	}
}

// --- Panic recovery ---

func TestPublish_PanicRecovery(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {
		panic("boom")
	})
	err := b.Publish(context.Background(), "evt", nil)
	if err == nil {
		t.Fatal("expected error from panicking handler")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error in chain, got %T", err)
	}
	if xe.Domain != DomainBus {
		t.Fatalf("domain = %q, want %q", xe.Domain, DomainBus)
	}
	if xe.Code != CodePublishFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodePublishFailed)
	}
}

func TestPublish_PanicDoesNotStopOtherHandlers(t *testing.T) {
	b := New()
	var called bool
	b.Subscribe("evt", func(context.Context, string, any) {
		panic("first handler panics")
	})
	b.Subscribe("evt", func(context.Context, string, any) {
		called = true
	})
	err := b.Publish(context.Background(), "evt", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !called {
		t.Fatal("second handler was not called after first panicked")
	}
}

func TestPublish_MultiplePanicsAggregated(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) { panic("p1") })
	b.Subscribe("evt", func(context.Context, string, any) { panic("p2") })
	b.Subscribe("evt", func(context.Context, string, any) {})

	err := b.Publish(context.Background(), "evt", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var me *errx.MultiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *errx.MultiError, got %T", err)
	}
	if me.Len() != 2 {
		t.Fatalf("MultiError.Len() = %d, want 2", me.Len())
	}
}

func TestPublish_PanicWithErrorValue(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {
		panic(errors.New("error-panic"))
	})
	err := b.Publish(context.Background(), "evt", nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

// --- Introspection ---

func TestSubscribers(t *testing.T) {
	b := New()
	if n := b.Subscribers("evt"); n != 0 {
		t.Fatalf("Subscribers = %d, want 0", n)
	}
	b.Subscribe("evt", func(context.Context, string, any) {})
	b.Subscribe("evt", func(context.Context, string, any) {})
	if n := b.Subscribers("evt"); n != 2 {
		t.Fatalf("Subscribers = %d, want 2", n)
	}
}

func TestSubscribers_OnClosedBus(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {})
	b.Close()
	if n := b.Subscribers("evt"); n != 0 {
		t.Fatalf("Subscribers = %d, want 0 after close", n)
	}
}

func TestEvents(t *testing.T) {
	b := New()
	if events := b.Events(); len(events) != 0 {
		t.Fatalf("Events = %v, want empty", events)
	}
	b.Subscribe("a", func(context.Context, string, any) {})
	b.Subscribe("b", func(context.Context, string, any) {})
	b.Subscribe("c", func(context.Context, string, any) {})
	events := b.Events()
	if len(events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(events))
	}
	seen := make(map[string]bool)
	for _, e := range events {
		seen[e] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Fatalf("event %q not found in %v", want, events)
		}
	}
}

func TestEvents_OnClosedBus(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {})
	b.Close()
	if events := b.Events(); events != nil {
		t.Fatalf("Events = %v, want nil after close", events)
	}
}

// --- Close / IsClosed ---

func TestClose(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {})
	if b.IsClosed() {
		t.Fatal("IsClosed = true before Close")
	}
	b.Close()
	if !b.IsClosed() {
		t.Fatal("IsClosed = false after Close")
	}
}

func TestClose_Idempotent(t *testing.T) {
	b := New()
	b.Close()
	b.Close()
	if !b.IsClosed() {
		t.Fatal("IsClosed = false after double Close")
	}
}

func TestClose_ClearsSubscriptions(t *testing.T) {
	b := New()
	b.Subscribe("a", func(context.Context, string, any) {})
	b.Subscribe("b", func(context.Context, string, any) {})
	b.Close()
	if events := b.Events(); events != nil {
		t.Fatalf("Events = %v, want nil after Close", events)
	}
}

// --- Thread safety ---

func TestConcurrent_SubscribePublish(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	const goroutines = 50
	const iterations = 100

	wg.Add(goroutines * 3)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range iterations {
				b.Subscribe("evt", func(context.Context, string, any) {})
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				b.Publish(context.Background(), "evt", nil)
			}
		}()
		go func() {
			defer wg.Done()
			for range iterations {
				b.Subscribers("evt")
				b.Events()
			}
		}()
	}
	wg.Wait()
}

func TestConcurrent_SubscribeUnsubscribe(t *testing.T) {
	b := New()
	var wg sync.WaitGroup
	const goroutines = 50

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range 100 {
				id, err := b.Subscribe("evt", func(context.Context, string, any) {})
				if err == nil {
					b.Unsubscribe(id)
				}
			}
		}()
	}
	wg.Wait()
}

func TestConcurrent_PublishWithClose(t *testing.T) {
	b := New()
	b.Subscribe("evt", func(context.Context, string, any) {})

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 1000 {
			b.Publish(context.Background(), "evt", nil)
		}
	}()
	go func() {
		defer wg.Done()
		b.Close()
	}()
	wg.Wait()
}

// --- Error structure ---

func TestErrClosed_Structure(t *testing.T) {
	b := New()
	b.Close()
	_, err := b.Subscribe("evt", func(context.Context, string, any) {})
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Domain != DomainBus {
		t.Fatalf("domain = %q, want %q", xe.Domain, DomainBus)
	}
	if xe.Code != CodeClosed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeClosed)
	}
}

func TestErrPublishFailed_HasEventMeta(t *testing.T) {
	b := New()
	b.Subscribe("user.created", func(context.Context, string, any) {
		panic("boom")
	})
	err := b.Publish(context.Background(), "user.created", nil)
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Meta["event"] != "user.created" {
		t.Fatalf("meta[event] = %v, want %q", xe.Meta["event"], "user.created")
	}
}

// --- Snapshot isolation ---

func TestPublish_SnapshotIsolation(t *testing.T) {
	b := New()
	var calls int
	b.Subscribe("evt", func(context.Context, string, any) {
		calls++
		b.Subscribe("evt", func(context.Context, string, any) {
			calls++
		})
	})
	_ = b.Publish(context.Background(), "evt", nil)
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (snapshot isolation)", calls)
	}
}

// --- Stats ---

func TestStats_Initial(t *testing.T) {
	b := New()
	s := b.Stats()
	if s.Events != 0 || s.Subscriptions != 0 || s.Published != 0 || s.Delivered != 0 || s.Panics != 0 {
		t.Fatalf("expected all zeros, got %+v", s)
	}
}

func TestStats_AfterSubscribe(t *testing.T) {
	b := New()
	b.Subscribe("a", func(ctx context.Context, event string, payload any) {})
	b.Subscribe("a", func(ctx context.Context, event string, payload any) {})
	b.Subscribe("b", func(ctx context.Context, event string, payload any) {})
	s := b.Stats()
	if s.Events != 2 {
		t.Fatalf("expected Events=2, got %d", s.Events)
	}
	if s.Subscriptions != 3 {
		t.Fatalf("expected Subscriptions=3, got %d", s.Subscriptions)
	}
}

func TestStats_AfterPublish(t *testing.T) {
	b := New()
	b.Subscribe("e", func(ctx context.Context, event string, payload any) {})
	b.Subscribe("e", func(ctx context.Context, event string, payload any) {})
	b.Publish(context.Background(), "e", nil)
	b.Publish(context.Background(), "e", nil)
	s := b.Stats()
	if s.Published != 2 {
		t.Fatalf("expected Published=2, got %d", s.Published)
	}
	if s.Delivered != 4 {
		t.Fatalf("expected Delivered=4 (2 handlers * 2 publishes), got %d", s.Delivered)
	}
}

func TestStats_PanicCounted(t *testing.T) {
	b := New()
	b.Subscribe("e", func(ctx context.Context, event string, payload any) {
		panic("boom")
	})
	b.Publish(context.Background(), "e", nil)
	s := b.Stats()
	if s.Panics != 1 {
		t.Fatalf("expected Panics=1, got %d", s.Panics)
	}
	if s.Delivered != 0 {
		t.Fatalf("expected Delivered=0 (panicked), got %d", s.Delivered)
	}
}

func TestStats_PublishNoSubscribers(t *testing.T) {
	b := New()
	b.Publish(context.Background(), "nobody", nil)
	s := b.Stats()
	if s.Published != 0 {
		t.Fatalf("expected Published=0 (no handlers), got %d", s.Published)
	}
}

func TestResetStats(t *testing.T) {
	b := New()
	b.Subscribe("e", func(ctx context.Context, event string, payload any) {})
	b.Publish(context.Background(), "e", nil)
	b.ResetStats()
	s := b.Stats()
	if s.Published != 0 || s.Delivered != 0 || s.Panics != 0 {
		t.Fatalf("expected all zeros after reset, got %+v", s)
	}
	if s.Events != 1 || s.Subscriptions != 1 {
		t.Fatalf("expected Events=1, Subscriptions=1 (not reset), got %+v", s)
	}
}

// --- WithOnError ---

func TestWithOnError_CalledOnPanic(t *testing.T) {
	var captured struct {
		event string
		err   error
	}
	b := New(WithOnError(func(event string, err error) {
		captured.event = event
		captured.err = err
	}))
	b.Subscribe("fail", func(ctx context.Context, event string, payload any) {
		panic("handler boom")
	})
	b.Publish(context.Background(), "fail", nil)
	if captured.event != "fail" {
		t.Fatalf("expected event='fail', got %q", captured.event)
	}
	if captured.err == nil {
		t.Fatal("expected non-nil error in onError callback")
	}
	var xe *errx.Error
	if !errors.As(captured.err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", captured.err)
	}
	if xe.Code != CodePublishFailed {
		t.Fatalf("expected code %s, got %s", CodePublishFailed, xe.Code)
	}
}

func TestWithOnError_NotCalledOnSuccess(t *testing.T) {
	called := false
	b := New(WithOnError(func(event string, err error) {
		called = true
	}))
	b.Subscribe("ok", func(ctx context.Context, event string, payload any) {})
	b.Publish(context.Background(), "ok", nil)
	if called {
		t.Fatal("onError should not be called when handler succeeds")
	}
}
