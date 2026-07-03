package shedx

import (
	"context"
	"testing"
)

// FuzzExecute drives the shedder with arbitrary capacity, threshold, and
// priority values. The invariants are: it never panics, in-flight returns to
// zero after every call completes, and a critical request is always admitted.
func FuzzExecute(f *testing.F) {
	f.Add(1000, 0.8, uint8(1))
	f.Add(0, 0.0, uint8(3))
	f.Add(-5, 2.0, uint8(99))
	f.Add(1, 0.01, uint8(0))

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, capacity int, threshold float64, prio uint8) {
		s := New(WithCapacity(capacity), WithThreshold(threshold))
		defer func() { _ = s.Close() }()

		priority := Priority(prio)
		_, err := Execute(s, ctx, priority,
			func(_ context.Context, sc ShedController) (int, error) {
				_ = sc.Load()
				_ = sc.Priority()
				_ = sc.Capacity()
				return 1, nil
			})

		if priority >= PriorityCritical && err != nil {
			t.Fatalf("critical request rejected: %v", err)
		}
		if got := s.InFlight(); got != 0 {
			t.Fatalf("in-flight not released: got %d", got)
		}
	})
}

// FuzzTryExecute drives the non-blocking admission path. The invariants match
// [FuzzExecute]: no panic, in-flight returns to zero, and critical requests
// always run.
func FuzzTryExecute(f *testing.F) {
	f.Add(1000, 0.8, uint8(1))
	f.Add(0, 0.0, uint8(3))
	f.Add(-5, 2.0, uint8(99))
	f.Add(1, 0.01, uint8(0))

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, capacity int, threshold float64, prio uint8) {
		s := New(WithCapacity(capacity), WithThreshold(threshold))
		defer func() { _ = s.Close() }()

		priority := Priority(prio)
		ok, _, err := TryExecute(s, ctx, priority,
			func(_ context.Context, sc ShedController) (int, error) {
				_ = sc.Load()
				_ = sc.Priority()
				_ = sc.Capacity()
				return 1, nil
			})

		if priority >= PriorityCritical {
			if !ok || err != nil {
				t.Fatalf("critical request rejected: ok=%v err=%v", ok, err)
			}
		}
		if got := s.InFlight(); got != 0 {
			t.Fatalf("in-flight not released: got %d", got)
		}
	})
}

// FuzzAcquireRelease verifies that any acquire/release sequence keeps the
// in-flight counter non-negative and back to zero once all tokens are freed.
func FuzzAcquireRelease(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3})
	f.Add([]byte{})
	f.Add([]byte{255, 255})

	f.Fuzz(func(t *testing.T, ops []byte) {
		s := New(WithCapacity(64), WithThreshold(0.5))
		defer func() { _ = s.Close() }()

		var tokens []*Token
		for _, op := range ops {
			if op%2 == 0 {
				if tok, err := s.Acquire(Priority(op % 4)); err == nil {
					tokens = append(tokens, tok)
				}
			} else if len(tokens) > 0 {
				tokens[len(tokens)-1].Release()
				tokens = tokens[:len(tokens)-1]
			}
			if s.InFlight() < 0 {
				t.Fatalf("in-flight went negative")
			}
		}
		for _, tok := range tokens {
			tok.Release()
		}
		if got := s.InFlight(); got != 0 {
			t.Fatalf("in-flight not zero after releasing all: got %d", got)
		}
	})
}
