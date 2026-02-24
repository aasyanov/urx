package retryx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/pkg/retryx"
)

func ExampleDo() {
	ctx := context.Background()

	result, err := retryx.Do(ctx, func(rc retryx.RetryController) (string, error) {
		if rc.Number() < 2 {
			return "", errors.New("not ready")
		}
		return "done", nil
	}, retryx.WithMaxAttempts(5), retryx.WithBackoff(10*time.Millisecond))

	fmt.Println(result, err)
	// Output: done <nil>
}

func ExampleDo_abort() {
	ctx := context.Background()

	_, err := retryx.Do(ctx, func(rc retryx.RetryController) (string, error) {
		rc.Abort()
		return "", errors.New("fatal")
	}, retryx.WithMaxAttempts(5))

	fmt.Println(err != nil)
	// Output: true
}
