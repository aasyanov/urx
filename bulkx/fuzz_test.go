package bulkx

import (
	"context"
	"testing"
	"time"
)

// FuzzExecute drives the bulkhead with arbitrary slot counts and timeouts. The
// invariants are: it never panics, the active count returns to zero after every
// completed call, and the controller snapshot stays within its bounds.
func FuzzExecute(f *testing.F) {
	f.Add(10, int64(time.Second))
	f.Add(0, int64(0))
	f.Add(-5, int64(-1))
	f.Add(1, int64(time.Millisecond))

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, maxConcurrent int, timeoutNanos int64) {
		b := New(WithMaxConcurrent(maxConcurrent), WithTimeout(time.Duration(timeoutNanos)))
		defer func() { _ = b.Close() }()

		_, err := Execute(b, ctx,
			func(_ context.Context, bc BulkController) (int, error) {
				if bc.Active() < 1 || bc.Active() > bc.MaxConcurrent() {
					t.Fatalf("active %d out of bounds [1, %d]", bc.Active(), bc.MaxConcurrent())
				}
				if l := bc.Load(); l <= 0 || l > 1 {
					t.Fatalf("load %v out of bounds (0, 1]", l)
				}
				return 1, nil
			})
		if err != nil {
			t.Fatalf("single uncontended Execute should always succeed: %v", err)
		}
		if got := b.Active(); got != 0 {
			t.Fatalf("active not released: got %d", got)
		}
	})
}

// FuzzAcquireRelease verifies that any acquire/release sequence keeps the active
// counter non-negative and back to zero once all tokens are freed.
func FuzzAcquireRelease(f *testing.F) {
	f.Add([]byte{0, 1, 0, 1})
	f.Add([]byte{})
	f.Add([]byte{1, 1, 1})

	f.Fuzz(func(t *testing.T, ops []byte) {
		b := New(WithMaxConcurrent(64), WithTimeout(time.Millisecond))
		defer func() { _ = b.Close() }()

		var tokens []*Token
		for _, op := range ops {
			if op%2 == 0 {
				if tok, err := b.Acquire(context.Background()); err == nil {
					tokens = append(tokens, tok)
				}
			} else if len(tokens) > 0 {
				tokens[len(tokens)-1].Release()
				tokens = tokens[:len(tokens)-1]
			}
			if b.Active() < 0 {
				t.Fatalf("active went negative")
			}
		}
		for _, tok := range tokens {
			tok.Release()
		}
		if got := b.Active(); got != 0 {
			t.Fatalf("active not zero after releasing all: got %d", got)
		}
	})
}
