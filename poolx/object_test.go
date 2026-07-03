package poolx

import (
	"bytes"
	"testing"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewObjectPool_NilFactoryPanics(t *testing.T) {
	assert.Panics(t, func() {
		NewObjectPool[*bytes.Buffer](nil)
	})
}

func TestObjectPool_GetCreatesWhenEmpty(t *testing.T) {
	var created int
	op := NewObjectPool(func() *bytes.Buffer {
		created++
		return new(bytes.Buffer)
	})

	buf := op.Get()
	require.NotNil(t, buf)
	assert.Equal(t, uint64(1), op.Stats().Gets)
	assert.GreaterOrEqual(t, op.Stats().Creates, uint64(1))
}

func TestObjectPool_PutThenGetReuses(t *testing.T) {
	op := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })

	buf := op.Get()
	op.Put(buf)
	assert.Equal(t, uint64(1), op.Stats().Puts)

	got := op.Get()
	assert.NotNil(t, got)
	assert.Equal(t, uint64(2), op.Stats().Gets)
}

func TestObjectPool_WithReset(t *testing.T) {
	op := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(b *bytes.Buffer) { b.Reset() }),
	)

	buf := op.Get()
	buf.WriteString("dirty data")
	require.Positive(t, buf.Len())

	op.Put(buf)
	assert.Equal(t, 0, buf.Len(), "reset hook must clear the object on Put")
}

func TestObjectPool_WithoutResetKeepsState(t *testing.T) {
	op := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })

	buf := op.Get()
	buf.WriteString("data")
	op.Put(buf)
	assert.Positive(t, buf.Len(), "without reset, state is left untouched")
}

func TestObjectPool_ResetStats(t *testing.T) {
	op := NewObjectPool(func() int { return 0 })
	_ = op.Get()
	op.Put(1)
	require.Positive(t, op.Stats().Gets)

	op.ResetStats()
	st := op.Stats()
	assert.Equal(t, uint64(0), st.Gets)
	assert.Equal(t, uint64(0), st.Puts)
	assert.Equal(t, uint64(0), st.Creates)
}

func TestObjectPool_ConcurrentGetPut(t *testing.T) {
	op := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(b *bytes.Buffer) { b.Reset() }),
	)

	testx.HammerVoid(50, 200, func() {
		buf := op.Get()
		buf.WriteString("x")
		op.Put(buf)
	})

	st := op.Stats()
	assert.Equal(t, uint64(50*200), st.Gets)
	assert.Equal(t, uint64(50*200), st.Puts)
}
