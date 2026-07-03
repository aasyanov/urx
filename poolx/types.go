package poolx

// WorkerStats is a point-in-time snapshot of [WorkerPool] counters returned
// by [WorkerPool.Stats].
type WorkerStats struct {
	// Workers is the configured number of worker goroutines.
	Workers int `json:"workers"`

	// QueueSize is the configured task queue capacity.
	QueueSize int `json:"queue_size"`

	// Pending is the number of tasks currently buffered in the queue.
	Pending int `json:"pending"`

	// Submitted is the total number of tasks successfully enqueued.
	Submitted uint64 `json:"submitted"`

	// Completed is the total number of tasks that returned without error.
	Completed uint64 `json:"completed"`

	// Failed is the total number of tasks that returned an error or panicked.
	Failed uint64 `json:"failed"`

	// Panics is the total number of tasks that panicked (a subset of Failed).
	Panics uint64 `json:"panics"`
}

// ObjectStats is a point-in-time snapshot of [ObjectPool] counters returned
// by [ObjectPool.Stats].
type ObjectStats struct {
	// Gets is the total number of [ObjectPool.Get] calls.
	Gets uint64 `json:"gets"`

	// Puts is the total number of [ObjectPool.Put] calls.
	Puts uint64 `json:"puts"`

	// Creates is the total number of objects created by the factory because
	// the pool was empty.
	Creates uint64 `json:"creates"`
}

// BatchStats is a point-in-time snapshot of [Batch] counters returned by
// [Batch.Stats].
type BatchStats struct {
	// BatchSize is the configured flush threshold.
	BatchSize int `json:"batch_size"`

	// FlushInterval is the configured periodic flush interval, formatted via
	// [time.Duration.String].
	FlushInterval string `json:"flush_interval"`

	// Buffered is the number of items currently held in the buffer.
	Buffered int `json:"buffered"`

	// Flushed is the total number of flush operations performed.
	Flushed uint64 `json:"flushed"`

	// Items is the total number of items flushed.
	Items uint64 `json:"items"`

	// Errors is the total number of flush operations that failed.
	Errors uint64 `json:"errors"`
}
