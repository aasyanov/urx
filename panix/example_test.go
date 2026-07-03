package panix_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/panix"
)

func ExampleSafe() {
	val, err := panix.Safe("example.divide", func() (int, error) {
		return 42 / 1, nil
	})
	fmt.Printf("val=%d, err=%v\n", val, err)
	// Output: val=42, err=<nil>
}

func ExampleSafe_panic() {
	_, err := panix.Safe("example.assert", func() (int, error) {
		var v any = "not an int"
		return v.(int), nil // type assertion panics
	})
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		fmt.Printf("recovered panic in %s\n", pe.Op)
	}
	// Output: recovered panic in example.assert
}

func ExampleSafeVoid() {
	err := panix.SafeVoid("example.init", func() error {
		panic("init failed")
	})
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		fmt.Printf("op=%s value=%v\n", pe.Op, pe.Value)
	}
	// Output: op=example.init value=init failed
}

func ExampleSafeGo() {
	done := make(chan error, 1)
	panix.SafeGo(context.Background(), "example.worker", func(_ context.Context) {
		panic("worker crashed")
	}, func(_ context.Context, err error) {
		done <- err
	})
	err := <-done
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		fmt.Printf("goroutine panic: %s\n", pe.Op)
	}
	// Output: goroutine panic: example.worker
}

func ExampleWrap() {
	risky := panix.Wrap("example.risky", func() (string, error) {
		panic("oops")
	})

	_, err := risky()
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		fmt.Printf("wrapped panic: %v\n", pe.Value)
	}
	// Output: wrapped panic: oops
}

func ExamplePanicError_Unwrap() {
	rootCause := errors.New("database connection lost")
	_, err := panix.Safe("example.query", func() (int, error) {
		panic(rootCause)
	})
	if errors.Is(err, rootCause) {
		fmt.Println("found root cause through Unwrap chain")
	}
	// Output: found root cause through Unwrap chain
}
