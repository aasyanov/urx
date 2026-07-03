package quotax_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/quotax"
)

// ExampleQuota_Allow shows non-blocking per-key admission: each key has its own
// independent token bucket.
func ExampleQuota_Allow() {
	q := quotax.New(quotax.WithRate(100), quotax.WithBurst(2))
	defer q.Close()

	fmt.Println(q.Allow("user:1")) // bucket for user:1
	fmt.Println(q.Allow("user:1"))
	fmt.Println(q.Allow("user:1")) // user:1 burst exhausted
	fmt.Println(q.Allow("user:2")) // user:2 has its own bucket
	// Output:
	// true
	// true
	// false
	// true
}

// ExampleExecute runs work under a key's bucket, degrading gracefully when the
// key is near its limit via the QuotaController.
func ExampleExecute() {
	q := quotax.New(quotax.WithRate(100), quotax.WithBurst(5))
	defer q.Close()

	got, err := quotax.Execute(q, context.Background(), "tenant:acme",
		func(_ context.Context, qc quotax.QuotaController) (int, error) {
			if qc.Tokens() < 1 {
				return 0, nil // degrade near the key's limit
			}
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleTryExecute shows the non-blocking execution variant for a single key.
func ExampleTryExecute() {
	q := quotax.New(quotax.WithRate(1), quotax.WithBurst(1))
	defer q.Close()

	ok, _, _ := quotax.TryExecute(q, context.Background(), "ip:10.0.0.1",
		func(context.Context, quotax.QuotaController) (string, error) {
			return "first", nil
		})
	fmt.Println("first admitted:", ok)

	ok, _, _ = quotax.TryExecute(q, context.Background(), "ip:10.0.0.1",
		func(context.Context, quotax.QuotaController) (string, error) {
			return "second", nil
		})
	fmt.Println("second admitted:", ok)
	// Output:
	// first admitted: true
	// second admitted: false
}

// ExampleQuotaController_SkipToken refunds the token for a no-op call so it does
// not count against the key's budget.
func ExampleQuotaController_SkipToken() {
	q := quotax.New(quotax.WithRate(1), quotax.WithBurst(1))
	defer q.Close()

	_, _ = quotax.Execute(q, context.Background(), "key",
		func(_ context.Context, qc quotax.QuotaController) (int, error) {
			qc.SkipToken() // cache hit: no downstream work performed
			return 0, nil
		})

	// The single token was refunded, so the next call for the key is admitted.
	fmt.Println(q.Allow("key"))
	// Output: true
}
