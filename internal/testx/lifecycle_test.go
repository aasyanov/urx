package testx

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCloser struct {
	closed atomic.Bool
}

func (f *fakeCloser) Close() error {
	f.closed.Store(true)
	return nil
}

func TestAssertCloseIdempotent_Success(t *testing.T) {
	AssertCloseIdempotent(t, &fakeCloser{})
}

var errClosed = errors.New("closed")

func TestAssertOpAfterClose_ReturnsExpectedError(t *testing.T) {
	AssertOpAfterClose(t, func() error {
		return errClosed
	}, errClosed, "Execute")
}

func TestAssertOpAfterClose_NilWantErr(t *testing.T) {
	AssertOpAfterClose(t, func() error {
		return errors.New("any error")
	}, nil, "Execute")
}

func TestAssertOpAfterClose_Integration(t *testing.T) {
	c := &fakeCloser{}
	err := c.Close()
	require.NoError(t, err)

	AssertOpAfterClose(t, func() error {
		if c.closed.Load() {
			return errClosed
		}
		return nil
	}, errClosed, "DoWork")

	assert.True(t, c.closed.Load())
}
