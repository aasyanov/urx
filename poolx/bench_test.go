package poolx

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func BenchmarkObjectPool_GetPut(b *testing.B) {
	op, err := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		buf := op.Get()
		op.Put(buf)
	}
}

func BenchmarkObjectPool_GetPut_WithReset(b *testing.B) {
	op, err := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(buf *bytes.Buffer) { buf.Reset() }),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for b.Loop() {
		buf := op.Get()
		op.Put(buf)
	}
}

func BenchmarkObjectPool_GetPut_Parallel(b *testing.B) {
	op, err := NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := op.Get()
			op.Put(buf)
		}
	})
}

func BenchmarkWorkerPool_Submit(b *testing.B) {
	wp := NewWorkerPool(WithWorkers(8), WithQueueSize(4096))
	defer func() { _ = wp.Close() }()
	ctx := context.Background()
	noop := func(context.Context) error { return nil }

	b.ResetTimer()
	for b.Loop() {
		_ = wp.Submit(ctx, noop)
	}
}

func BenchmarkObjectPool_GetPut_WithReset_Parallel(b *testing.B) {
	op, err := NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		WithReset(func(buf *bytes.Buffer) { buf.Reset() }),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			buf := op.Get()
			op.Put(buf)
		}
	})
}

func BenchmarkWorkerPool_SubmitWait(b *testing.B) {
	wp := NewWorkerPool(WithWorkers(4), WithQueueSize(1024))
	defer func() { _ = wp.Close() }()
	ctx := context.Background()
	noop := func(context.Context) error { return nil }

	b.ResetTimer()
	for b.Loop() {
		_ = wp.SubmitWait(ctx, noop)
	}
}

func BenchmarkWorkerPool_Submit_Parallel(b *testing.B) {
	wp := NewWorkerPool(WithWorkers(8), WithQueueSize(4096))
	defer func() { _ = wp.Close() }()
	ctx := context.Background()
	noop := func(context.Context) error { return nil }

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = wp.SubmitWait(ctx, noop)
		}
	})
}

func BenchmarkBatch_Add(b *testing.B) {
	batch, err := NewBatch(func(context.Context, []int) error { return nil },
		WithBatchSize(1024), WithFlushInterval(time.Hour))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = batch.Close() }()

	b.ResetTimer()
	for b.Loop() {
		_ = batch.Add(1)
	}
}

func BenchmarkBatch_Add_Parallel(b *testing.B) {
	batch, err := NewBatch(func(context.Context, []int) error { return nil },
		WithBatchSize(1024), WithFlushInterval(time.Hour))
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = batch.Close() }()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = batch.Add(1)
		}
	})
}
