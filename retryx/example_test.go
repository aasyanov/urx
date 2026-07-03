package retryx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/retryx"
)

// ExampleDo demonstrates retrying a flaky operation until it succeeds.
func ExampleDo() {
	attempts := 0
	got, err := retryx.Do(context.Background(), func(context.Context, retryx.RetryController) (string, error) {
		attempts++
		if attempts < 3 {
			return "", errors.New("temporary failure")
		}
		return "success", nil
	}, retryx.WithBackoff(time.Millisecond), retryx.WithJitter(false))

	fmt.Println(got, err)
	fmt.Println("attempts:", attempts)
	// Output:
	// success <nil>
	// attempts: 3
}

// ExampleDo_abort shows aborting the retry loop on a permanent error.
func ExampleDo_abort() {
	permanent := errors.New("invalid credentials")
	attempts := 0
	_, err := retryx.Do(context.Background(), func(_ context.Context, rc retryx.RetryController) (int, error) {
		attempts++
		rc.Abort() // do not retry; this error is permanent
		return 0, permanent
	}, retryx.WithMaxAttempts(5))

	fmt.Println("aborted:", errors.Is(err, retryx.ErrAborted))
	fmt.Println("attempts:", attempts)
	// Output:
	// aborted: true
	// attempts: 1
}

// ExampleDo_retryIf shows restricting retries to a class of errors.
func ExampleDo_retryIf() {
	permanent := errors.New("permanent")
	attempts := 0
	_, err := retryx.Do(context.Background(), func(context.Context, retryx.RetryController) (int, error) {
		attempts++
		return 0, permanent
	},
		retryx.WithMaxAttempts(5),
		retryx.WithRetryIf(func(err error) bool { return !errors.Is(err, permanent) }),
	)

	fmt.Println("exhausted:", errors.Is(err, retryx.ErrExhausted))
	fmt.Println("attempts:", attempts)
	// Output:
	// exhausted: true
	// attempts: 1
}

// ExampleDo_controller shows adapting behaviour using the RetryController's
// view of the previous error and attempt number.
func ExampleDo_controller() {
	_, _ = retryx.Do(context.Background(), func(_ context.Context, rc retryx.RetryController) (int, error) {
		if rc.Number() == 1 {
			return 0, errors.New("first failure")
		}
		fmt.Println("retrying after:", rc.LastErr())
		return 1, nil
	}, retryx.WithBackoff(time.Millisecond), retryx.WithJitter(false))
	// Output:
	// retrying after: first failure
}
