package testx

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHammer_NoErrors(t *testing.T) {
	var count atomic.Int64
	errs := Hammer(10, 100, func() error {
		count.Add(1)
		return nil
	})
	assert.Empty(t, errs)
	assert.Equal(t, int64(1000), count.Load())
}

func TestHammer_AllErrors(t *testing.T) {
	sentinel := errors.New("fail")
	errs := Hammer(5, 10, func() error {
		return sentinel
	})
	assert.Len(t, errs, 50)
	for _, err := range errs {
		assert.ErrorIs(t, err, sentinel)
	}
}

func TestHammer_PartialErrors(t *testing.T) {
	var count atomic.Int64
	errs := Hammer(10, 100, func() error {
		n := count.Add(1)
		if n%10 == 0 {
			return errors.New("every 10th")
		}
		return nil
	})
	assert.Len(t, errs, 100, "100 out of 1000 calls should fail")
}

func TestHammerNoError(t *testing.T) {
	HammerNoError(t, 10, 100, func() error { return nil })
}

func TestHammerVoid(t *testing.T) {
	var count atomic.Int64
	HammerVoid(10, 100, func() {
		count.Add(1)
	})
	assert.Equal(t, int64(1000), count.Load())
}

func TestHammerIndexed(t *testing.T) {
	errs := HammerIndexed(5, 10, func(idx int) error {
		if idx == 0 {
			return errors.New("goroutine 0 fails")
		}
		return nil
	})
	assert.Len(t, errs, 10, "only goroutine 0 should fail (10 iters)")
}
