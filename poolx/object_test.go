package poolx

import (
	"bytes"
	"testing"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewObjectPool_NilFactoryReturnsErrNilFactory(t *testing.T) {
	_, err := NewObjectPool[*bytes.Buffer](nil)
	require.ErrorIs(t, err, ErrNilFactory)
}

func TestObjectPool_GetCreatesWhenEmpty(t *testing.T) {
	op, err := NewObjectPool(func() *bytes.Buffer {
		return new(bytes.Buffer)
	})
	require.NoError(t, err)

	buf := op.Get()
	require.NotNil(t, buf)
	assert.Equal(t, uint64(1), op.Stats().Gets)
	assert.GreaterOrEqual(t, op.Stats().Creates, uint64(1))
}

func TestObjectPool_PutThenGetReuses(t *testing.T) {
	op, err := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
	require.NoError(t, err)

	buf := op.Get()
	op.Put(buf)
	assert.Equal(t, uint64(1), op.Stats().Puts)

	got := op.Get()
	assert.NotNil(t, got)
	assert.Equal(t, uint64(2), op.Stats().Gets)
}

func TestObjectPool_WithReset(t *testing.T) {
	op, err := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(b *bytes.Buffer) { b.Reset() }),
	)
	require.NoError(t, err)

	buf := op.Get()
	buf.WriteString("dirty data")
	require.Positive(t, buf.Len())

	op.Put(buf)
	assert.Equal(t, 0, buf.Len(), "reset hook must clear the object on Put")
}

func TestObjectPool_WithoutResetKeepsState(t *testing.T) {
	op, err := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
	require.NoError(t, err)

	buf := op.Get()
	buf.WriteString("data")
	op.Put(buf)
	assert.Positive(t, buf.Len(), "without reset, state is left untouched")
}

func TestObjectPool_ResetStats(t *testing.T) {
	op, err := NewObjectPool(func() int { return 0 })
	require.NoError(t, err)

	_ = op.Get()
	op.Put(1)
	require.Positive(t, op.Stats().Gets)

	op.ResetStats()
	st := op.Stats()
	assert.Equal(t, uint64(0), st.Gets)
	assert.Equal(t, uint64(0), st.Puts)
	assert.Equal(t, uint64(0), st.Creates)
}

func TestObjectPool_Options(t *testing.T) {
	tests := []struct {
		name      string
		opts      []ObjectOption[*bytes.Buffer]
		wantReset bool
	}{
		{name: "defaults", opts: nil, wantReset: false},
		{name: "with reset", opts: []ObjectOption[*bytes.Buffer]{
			WithReset(func(b *bytes.Buffer) { b.Reset() }),
		}, wantReset: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, err := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) }, tt.opts...)
			require.NoError(t, err)

			buf := op.Get()
			buf.WriteString("x")
			op.Put(buf)
			if tt.wantReset {
				assert.Equal(t, 0, buf.Len())
			} else {
				assert.Positive(t, buf.Len())
			}
		})
	}
}

func TestObjectPool_ConcurrentGetPut(t *testing.T) {
	op, err := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(b *bytes.Buffer) { b.Reset() }),
	)
	require.NoError(t, err)

	testx.HammerVoid(50, 200, func() {
		buf := op.Get()
		buf.WriteString("x")
		op.Put(buf)
	})

	st := op.Stats()
	assert.Equal(t, uint64(50*200), st.Gets)
	assert.Equal(t, uint64(50*200), st.Puts)
}
