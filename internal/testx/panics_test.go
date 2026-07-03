package testx

import (
	"errors"
	"testing"

	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
)

func TestRequirePanicError_Success(t *testing.T) {
	_, err := panix.Safe("test.op", func() (int, error) {
		panic("boom")
	})
	pe := RequirePanicError(t, err, "test.op")
	assert.Equal(t, "boom", pe.Value)
	assert.NotEmpty(t, pe.Stack)
}

func TestRequirePanicError_WithErrorPanicValue(t *testing.T) {
	cause := errors.New("root")
	_, err := panix.Safe("err.op", func() (int, error) {
		panic(cause)
	})
	pe := RequirePanicError(t, err, "err.op")
	assert.Equal(t, cause, pe.Value)
}

func TestAssertNotPanicError_RegularError(t *testing.T) {
	AssertNotPanicError(t, errors.New("regular"))
}

func TestAssertNotPanicError_NilError(t *testing.T) {
	AssertNotPanicError(t, nil)
}
