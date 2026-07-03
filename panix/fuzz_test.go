package panix

import (
	"errors"
	"testing"
)

func FuzzSafe(f *testing.F) {
	f.Add("op", "panic value")
	f.Add("", "")
	f.Add("a.b.c", "multi\nline\npanic")
	f.Add("unicode.op", "пример паники 🔥")
	f.Fuzz(func(t *testing.T, op, panicVal string) {
		val, err := Safe(op, func() (string, error) {
			panic(panicVal)
		})
		if val != "" {
			t.Errorf("expected zero value, got %q", val)
		}
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("expected *PanicError, got %T: %v", err, err)
		}
		if pe.Op != op {
			t.Errorf("Op = %q, want %q", pe.Op, op)
		}
		if pe.Value != panicVal {
			t.Errorf("Value = %v, want %q", pe.Value, panicVal)
		}
		if len(pe.Stack) == 0 {
			t.Error("Stack is empty")
		}
		msg := pe.Error()
		if msg == "" {
			t.Error("Error() returned empty string")
		}
	})
}

func FuzzSafeVoid(f *testing.F) {
	f.Add("op", true)
	f.Add("", false)
	f.Add("deep.nested.op", true)
	f.Fuzz(func(t *testing.T, op string, shouldPanic bool) {
		err := SafeVoid(op, func() error {
			if shouldPanic {
				panic(op)
			}
			return nil
		})
		if shouldPanic {
			var pe *PanicError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *PanicError, got %T: %v", err, err)
			}
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func FuzzWrap(f *testing.F) {
	f.Add("op", "panic value")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, op, panicVal string) {
		wrapped := Wrap(op, func() (string, error) {
			panic(panicVal)
		})
		val, err := wrapped()
		if val != "" {
			t.Errorf("expected zero value, got %q", val)
		}
		var pe *PanicError
		if !errors.As(err, &pe) {
			t.Fatalf("expected *PanicError, got %T: %v", err, err)
		}
		if pe.Op != op {
			t.Errorf("Op = %q, want %q", pe.Op, op)
		}
		if len(pe.Stack) == 0 {
			t.Error("Stack is empty")
		}
	})
}

func FuzzWrapVoid(f *testing.F) {
	f.Add("op", true)
	f.Add("", false)
	f.Fuzz(func(t *testing.T, op string, shouldPanic bool) {
		wrapped := WrapVoid(op, func() error {
			if shouldPanic {
				panic(op)
			}
			return nil
		})
		err := wrapped()
		if shouldPanic {
			var pe *PanicError
			if !errors.As(err, &pe) {
				t.Fatalf("expected *PanicError, got %T: %v", err, err)
			}
		} else if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
