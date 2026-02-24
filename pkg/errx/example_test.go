package errx_test

import (
	"errors"
	"fmt"

	"github.com/aasyanov/urx/pkg/errx"
)

func ExampleNew() {
	err := errx.New("AUTH", "UNAUTHORIZED", "token expired",
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithRetry(errx.RetryUnsafe),
	)

	fmt.Println(err.Domain, err.Code, err.Message)
	// Output: AUTH UNAUTHORIZED token expired
}

func ExampleWrap() {
	cause := errors.New("connection refused")
	err := errx.Wrap(cause, "REPO", "INTERNAL", "db unavailable",
		errx.WithOp("UserRepo.FindByID"),
	)

	fmt.Println(err.Op, err.Domain)
	// Output: UserRepo.FindByID REPO
}

func ExampleAs() {
	original := errx.New("AUTH", "FORBIDDEN", "denied")
	var wrapped error = original

	if ex, ok := errx.As(wrapped); ok {
		fmt.Println(ex.Code)
	}
	// Output: FORBIDDEN
}
