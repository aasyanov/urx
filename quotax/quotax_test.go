package quotax

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/aasyanov/urx/ratex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Defaults(t *testing.T) {
	q := New()
	defer q.Close()

	assert.Len(t, q.shards, DefaultShards)
	assert.Equal(t, DefaultRate, q.cfg.rate)
	assert.Equal(t, DefaultBurst, q.cfg.burst)
	assert.Equal(t, int64(unlimitedKeys), q.cfg.maxKeys)
	assert.Equal(t, DefaultEvictionTTL, q.cfg.evictionTTL)
	assert.Equal(t, DefaultEvictionInterval, q.cfg.evictionInterval)
}

func TestWithShards(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int
	}{
		{"default", nil, DefaultShards},
		{"custom", WithShards(8), 8},
		{"non-positive ignored", WithShards(-5), DefaultShards},
		{"zero ignored", WithShards(0), DefaultShards},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Len(t, q.shards, tt.want)
		})
	}
}

func TestWithRate(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want float64
	}{
		{"default", nil, DefaultRate},
		{"custom", WithRate(250), 250},
		{"zero ignored", WithRate(0), DefaultRate},
		{"negative ignored", WithRate(-5), DefaultRate},
		{"fractional", WithRate(0.5), 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Equal(t, tt.want, q.cfg.rate)
		})
	}
}

func TestWithBurst(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int
	}{
		{"default", nil, DefaultBurst},
		{"custom", WithBurst(100), 100},
		{"zero clamped to floor", WithBurst(0), minBurst},
		{"negative clamped to floor", WithBurst(-3), minBurst},
		{"floor explicit", WithBurst(1), 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Equal(t, tt.want, q.cfg.burst)
		})
	}
}

func TestWithMaxKeys(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want int64
	}{
		{"default unlimited", nil, unlimitedKeys},
		{"custom", WithMaxKeys(100), 100},
		{"zero means unlimited", WithMaxKeys(0), unlimitedKeys},
		{"negative ignored", WithMaxKeys(-1), unlimitedKeys},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Equal(t, tt.want, q.cfg.maxKeys)
		})
	}
}

func TestWithEvictionTTL(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{"default", nil, DefaultEvictionTTL},
		{"custom", WithEvictionTTL(time.Hour), time.Hour},
		{"zero ignored", WithEvictionTTL(0), DefaultEvictionTTL},
		{"negative ignored", WithEvictionTTL(-time.Second), DefaultEvictionTTL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Equal(t, tt.want, q.cfg.evictionTTL)
		})
	}
}

func TestWithEvictionInterval(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{"default", nil, DefaultEvictionInterval},
		{"custom", WithEvictionInterval(30 * time.Second), 30 * time.Second},
		{"zero ignored", WithEvictionInterval(0), DefaultEvictionInterval},
		{"negative ignored", WithEvictionInterval(-time.Minute), DefaultEvictionInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			q := New(opts...)
			defer q.Close()
			assert.Equal(t, tt.want, q.cfg.evictionInterval)
		})
	}
}

func TestNewConfig_PreservesFractionalRate(t *testing.T) {
	q := New(WithRate(0.5))
	defer q.Close()
	assert.Equal(t, 0.5, q.cfg.rate)
	b := q.getOrCreate(q.shardFor("k"), "k")
	assert.Equal(t, 0.5, b.limiter.Rate())
}

func TestNewConfig_SkipsNilOption(t *testing.T) {
	cfg := newConfig([]Option{
		WithRate(5),
		nil,
		WithBurst(3),
	})
	assert.Equal(t, 5.0, cfg.rate)
	assert.Equal(t, 3, cfg.burst)
}

func TestQuota_Allow_PerKeyIsolation(t *testing.T) {
	q := New(WithRate(1), WithBurst(2))
	defer q.Close()

	assert.True(t, q.Allow("a"))
	assert.True(t, q.Allow("a"))
	assert.False(t, q.Allow("a"), "key a burst exhausted")

	assert.True(t, q.Allow("b"), "key b has an independent bucket")
	assert.True(t, q.Allow("b"))
	assert.False(t, q.Allow("b"))
}

func TestQuota_AllowN(t *testing.T) {
	q := New(WithRate(1), WithBurst(10))
	defer q.Close()

	assert.True(t, q.AllowN("k", 10))
	assert.False(t, q.AllowN("k", 1))
}

func TestQuota_AllowN_ExceedsBurst(t *testing.T) {
	q := New(WithRate(10), WithBurst(3))
	defer q.Close()

	assert.False(t, q.AllowN("k", 5))
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestQuota_Allow_AfterClose(t *testing.T) {
	q := New()
	require.NoError(t, q.Close())
	assert.False(t, q.Allow("k"))
}

func TestQuota_AllowOrError(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() *Quota
		key     string
		wantErr error
	}{
		{
			name:    "admitted returns nil",
			setup:   func() *Quota { return New(WithRate(1), WithBurst(1)) },
			key:     "k",
			wantErr: nil,
		},
		{
			name: "exhausted returns ErrLimited",
			setup: func() *Quota {
				q := New(WithRate(0.001), WithBurst(1))
				q.Allow("k") // drain the only token
				return q
			},
			key:     "k",
			wantErr: ErrLimited,
		},
		{
			name: "closed returns ErrClosed",
			setup: func() *Quota {
				q := New()
				_ = q.Close()
				return q
			},
			key:     "k",
			wantErr: ErrClosed,
		},
		{
			name: "over max keys returns ErrMaxKeys",
			setup: func() *Quota {
				q := New(WithMaxKeys(1))
				q.Allow("existing")
				return q
			},
			key:     "new",
			wantErr: ErrMaxKeys,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.setup()
			defer q.Close()
			err := q.AllowOrError(tt.key)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestQuota_MaxKeys_Enforced(t *testing.T) {
	var rejected atomic.Int64
	q := New(
		WithMaxKeys(2),
		WithOnMaxKeys(func(string) { rejected.Add(1) }),
	)
	defer q.Close()

	assert.True(t, q.Allow("a"))
	assert.True(t, q.Allow("b"))
	assert.False(t, q.Allow("c"), "third distinct key exceeds cap")
	assert.Equal(t, int64(2), q.KeyCount())
	assert.Equal(t, int64(1), rejected.Load())

	assert.True(t, q.Allow("a"), "existing key still admitted after cap")
}

func TestQuota_OnMaxKeys_RecoversPanic(t *testing.T) {
	q := New(
		WithMaxKeys(1),
		WithOnMaxKeys(func(string) { panic("hook boom") }),
	)
	defer q.Close()

	require.True(t, q.Allow("a"))
	assert.False(t, q.Allow("b"), "panic in OnMaxKeys must not crash; new key still rejected")
	assert.Equal(t, int64(1), q.KeyCount())
}

func TestQuota_Wait_Succeeds(t *testing.T) {
	q := New(WithRate(1000), WithBurst(1))
	defer q.Close()

	require.NoError(t, q.Wait(context.Background(), "k"))
}

func TestQuota_Wait_BlocksThenAdmits(t *testing.T) {
	q := New(WithRate(1000), WithBurst(1))
	defer q.Close()

	require.NoError(t, q.Wait(context.Background(), "k")) // drain
	start := time.Now()
	require.NoError(t, q.Wait(context.Background(), "k")) // must wait for refill
	assert.GreaterOrEqual(t, time.Since(start), minWaitDelay)
}

func TestQuota_WaitN_Cancelled(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))
	defer q.Close()

	q.Allow("k") // drain the bucket so Wait must block

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := q.WaitN(ctx, "k", 1)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestQuota_WaitN_CancelAfterTimerFires(t *testing.T) {
	q := New(WithRate(10), WithBurst(1))
	defer q.Close()

	require.True(t, q.Allow("k"))

	err := q.WaitN(&cancelAfterCtx{after: 2}, "k", 1)
	require.ErrorIs(t, err, ErrCancelled)
	s := q.Stats()
	assert.Equal(t, int64(1), s.Limited)
	assert.Equal(t, int64(1), s.Allowed, "initial Allow only")
}

func TestQuota_Wait_AlreadyCancelled(t *testing.T) {
	q := New()
	defer q.Close()
	err := q.Wait(testx.CancelledCtx(), "k")
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestQuota_WaitN_ExceedsBurstReturnsImmediately(t *testing.T) {
	q := New(WithRate(1000), WithBurst(2))
	defer q.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	start := time.Now()
	err := q.WaitN(ctx, "k", 5)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, ratex.ErrExceedsBurst)
	assert.Less(t, elapsed, 50*time.Millisecond, "must return in milliseconds, not wait out the context")
	assert.Equal(t, int64(1), q.Stats().Limited)
	assert.Equal(t, int64(0), q.KeyCount(), "fail-fast must not create a key")
}

func TestQuota_WaitN_AbortsOnClose(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))

	q.Allow("k") // drain so Wait must block

	done := make(chan error, 1)
	go func() { done <- q.Wait(context.Background(), "k") }()

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, q.Close())
	require.ErrorIs(t, <-done, ErrClosed)
}

func TestQuota_Wait_AfterClose(t *testing.T) {
	q := New()
	require.NoError(t, q.Close())
	require.ErrorIs(t, q.Wait(context.Background(), "k"), ErrClosed)
}

func TestQuota_Wait_MaxKeys(t *testing.T) {
	q := New(WithMaxKeys(1))
	defer q.Close()
	q.Allow("existing")
	require.ErrorIs(t, q.Wait(context.Background(), "new"), ErrMaxKeys)
}

func TestWaitN_NonPositiveNTreatedAsOne(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	require.NoError(t, q.WaitN(context.Background(), "k", 0))
}

func TestWaitN_CancelImmediatelyAfterAcquire(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	err := q.WaitN(&cancelAfterCtx{after: 2}, "k", 1)
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestWaitForOnBucket_RejectsWhenClosed(t *testing.T) {
	q := New()
	b := q.getOrCreate(q.shardFor("k"), "k")
	require.NoError(t, q.Close())
	_, err := q.waitForOnBucket(context.Background(), b, 1)
	require.ErrorIs(t, err, ErrClosed)
}

func TestWaitForOnBucket_RejectsCancelledContextAtEntry(t *testing.T) {
	q := New()
	defer q.Close()
	b := q.getOrCreate(q.shardFor("k"), "k")
	_, err := q.waitForOnBucket(testx.CancelledCtx(), b, 1)
	require.ErrorIs(t, err, ErrCancelled)
}

func TestExecute_RunsAndCounts(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	got, err := Execute(q, context.Background(), "k",
		func(_ context.Context, qc QuotaController) (int, error) {
			assert.Equal(t, "k", qc.Key())
			assert.Equal(t, 1000.0, qc.Rate())
			assert.Equal(t, 5, qc.Burst())
			assert.False(t, qc.Waited())
			return 42, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, int64(1), q.Stats().Allowed)
}

func TestExecute_NilFunc(t *testing.T) {
	q := New()
	defer q.Close()
	_, err := Execute[int](q, context.Background(), "k", nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_AfterClose(t *testing.T) {
	q := New()
	require.NoError(t, q.Close())
	_, err := Execute(q, context.Background(), "k",
		func(context.Context, QuotaController) (int, error) { return 0, nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestExecute_MaxKeys(t *testing.T) {
	q := New(WithMaxKeys(1))
	defer q.Close()
	q.Allow("existing")
	_, err := Execute(q, context.Background(), "new",
		func(context.Context, QuotaController) (int, error) { return 0, nil })
	require.ErrorIs(t, err, ErrMaxKeys)
}

func TestExecute_Cancelled(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))
	defer q.Close()
	q.Allow("k") // drain

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := Execute(q, ctx, "k",
		func(context.Context, QuotaController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestExecute_AbortsOnClose(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))
	q.Allow("k") // drain

	done := make(chan error, 1)
	go func() {
		_, err := Execute(q, context.Background(), "k",
			func(context.Context, QuotaController) (int, error) { return 1, nil })
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	require.NoError(t, q.Close())
	require.ErrorIs(t, <-done, ErrClosed)
}

func TestExecute_PanicBecomesError(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	_, err := Execute(q, context.Background(), "k",
		func(context.Context, QuotaController) (int, error) { panic("boom") })

	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, opExecute, pe.Op, "panic must be attributed to quotax, not ratex")
	assert.Equal(t, "boom", pe.Value)
}

func TestTryExecute_PanicBecomesError(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	ok, _, err := TryExecute(q, context.Background(), "k",
		func(context.Context, QuotaController) (int, error) { panic("kaboom") })

	assert.True(t, ok, "token was available, so fn ran")
	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, opTryExecute, pe.Op, "panic must be attributed to quotax, not ratex")
}

func TestExecute_PropagatesCallbackError(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	sentinel := errors.New("callback failed")
	_, err := Execute(q, context.Background(), "k",
		func(context.Context, QuotaController) (int, error) { return 0, sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int64(1), q.Stats().Allowed, "callback error still consumed a token")
}

func TestExecute_SkipToken_RefundsKeyBucket(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))
	defer q.Close()

	_, err := Execute(q, context.Background(), "k",
		func(_ context.Context, qc QuotaController) (int, error) {
			qc.SkipToken()
			return 0, nil
		})
	require.NoError(t, err)

	assert.True(t, q.Allow("k"), "token was refunded to the key's bucket")
}

func TestTryExecute_AdmitsThenRejects(t *testing.T) {
	q := New(WithRate(0.0001), WithBurst(1))
	defer q.Close()

	ok, val, err := TryExecute(q, context.Background(), "k",
		func(context.Context, QuotaController) (string, error) { return "first", nil })
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "first", val)

	ok, _, err = TryExecute(q, context.Background(), "k",
		func(context.Context, QuotaController) (string, error) { return "second", nil })
	require.NoError(t, err)
	assert.False(t, ok, "no token available, fn not run")
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestTryExecute_AlreadyCancelled(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	ok, _, err := TryExecute(q, testx.CancelledCtx(), "k",
		func(context.Context, QuotaController) (int, error) { return 1, nil })
	assert.False(t, ok)
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(0), q.Stats().Limited, "cancelled ctx must not count as limited")
}

func TestTryExecute_CancelledAfterAllow(t *testing.T) {
	q := New(WithRate(1000), WithBurst(5))
	defer q.Close()

	ok, _, err := TryExecute(q, &cancelAfterCtx{after: 2}, "k",
		func(context.Context, QuotaController) (int, error) { return 1, nil })
	assert.False(t, ok)
	require.ErrorIs(t, err, ErrCancelled)
	assert.Equal(t, int64(1), q.Stats().Limited)
}

func TestExecute_WaitsThenAdmits(t *testing.T) {
	q := New(WithRate(1000), WithBurst(1))
	defer q.Close()

	require.NoError(t, q.Wait(context.Background(), "k")) // drain

	var waited bool
	_, err := Execute(q, context.Background(), "k",
		func(_ context.Context, qc QuotaController) (int, error) {
			waited = qc.Waited()
			return 1, nil
		})
	require.NoError(t, err)
	assert.True(t, waited)
}

func TestTryExecute_NilFunc(t *testing.T) {
	q := New()
	defer q.Close()
	ok, _, err := TryExecute[int](q, context.Background(), "k", nil)
	assert.False(t, ok)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestTryExecute_AfterClose(t *testing.T) {
	q := New()
	require.NoError(t, q.Close())
	ok, _, err := TryExecute(q, context.Background(), "k",
		func(context.Context, QuotaController) (int, error) { return 0, nil })
	assert.False(t, ok)
	require.ErrorIs(t, err, ErrClosed)
}

func TestTryExecute_MaxKeys(t *testing.T) {
	q := New(WithMaxKeys(1))
	defer q.Close()
	q.Allow("existing")
	ok, _, err := TryExecute(q, context.Background(), "new",
		func(context.Context, QuotaController) (int, error) { return 0, nil })
	assert.False(t, ok)
	require.ErrorIs(t, err, ErrMaxKeys)
}

func TestQuota_RemoveAndExists(t *testing.T) {
	q := New()
	defer q.Close()

	q.Allow("k")
	assert.True(t, q.Exists("k"))
	assert.Equal(t, int64(1), q.KeyCount())

	assert.True(t, q.Remove("k"))
	assert.False(t, q.Exists("k"))
	assert.Equal(t, int64(0), q.KeyCount())

	assert.False(t, q.Remove("k"), "removing an absent key reports false")
}

func TestQuota_Reset(t *testing.T) {
	q := New()
	defer q.Close()

	q.Allow("a")
	q.Allow("b")
	assert.Equal(t, int64(2), q.KeyCount())

	q.Reset()
	assert.Equal(t, int64(0), q.KeyCount())
	assert.False(t, q.Exists("a"))
}

func TestQuota_ResetStats(t *testing.T) {
	q := New(WithRate(1), WithBurst(1))
	defer q.Close()

	q.Allow("k")
	q.Allow("k") // limited
	s := q.Stats()
	assert.Equal(t, int64(1), s.Allowed)
	assert.Equal(t, int64(1), s.Limited)

	q.ResetStats()
	s = q.Stats()
	assert.Equal(t, int64(0), s.Allowed)
	assert.Equal(t, int64(0), s.Limited)
	assert.Equal(t, int64(1), s.Keys, "ResetStats leaves key count untouched")
}

func TestQuota_Eviction(t *testing.T) {
	q := New(
		WithEvictionTTL(time.Millisecond),
		WithEvictionInterval(time.Hour), // disable the background pass; drive manually
	)
	defer q.Close()

	q.Allow("k")
	assert.Equal(t, int64(1), q.KeyCount())

	time.Sleep(5 * time.Millisecond)
	q.ForceEviction()
	assert.Equal(t, int64(0), q.KeyCount())
	assert.False(t, q.Exists("k"))
}

func TestQuota_Eviction_KeepsActiveKey(t *testing.T) {
	q := New(
		WithEvictionTTL(time.Hour),
		WithEvictionInterval(time.Hour),
	)
	defer q.Close()

	q.Allow("k")
	q.ForceEviction()
	assert.True(t, q.Exists("k"), "recently active key survives eviction")
}

func TestQuota_Wait_PinnedNotEvicted(t *testing.T) {
	q := New(
		WithRate(0.0001),
		WithBurst(1),
		WithEvictionTTL(time.Millisecond),
		WithEvictionInterval(time.Hour),
		WithShards(1),
	)
	defer q.Close()

	require.True(t, q.Allow("k")) // drain burst so Wait blocks
	s := q.shardFor("k")
	s.mu.RLock()
	b := s.buckets["k"]
	s.mu.RUnlock()
	require.NotNil(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- q.Wait(ctx, "k") }()

	testx.Eventually(t, func() bool { return b.pins.Load() > 0 }, time.Second)
	time.Sleep(5 * time.Millisecond)
	q.ForceEviction()

	assert.True(t, q.Exists("k"))
	assert.Equal(t, int64(1), q.KeyCount())

	cancel()
	require.ErrorIs(t, <-errCh, ErrCancelled)
}

func TestQuota_Execute_LongFn_NoGhostBucket(t *testing.T) {
	q := New(
		WithRate(0.0001),
		WithBurst(2),
		WithEvictionTTL(time.Millisecond),
		WithEvictionInterval(time.Hour),
	)
	defer q.Close()

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Execute(q, context.Background(), "k",
			func(context.Context, QuotaController) (int, error) {
				close(started)
				time.Sleep(20 * time.Millisecond)
				return 1, nil
			})
		done <- err
	}()
	<-started
	q.ForceEviction()

	admitted := 0
	for range 5 {
		if q.Allow("k") {
			admitted++
		}
	}
	assert.Equal(t, 1, admitted, "same limiter: burst 2 minus Execute's 1 token; a ghost bucket would admit a full burst")
	require.NoError(t, <-done)
	assert.Equal(t, int64(1), q.KeyCount())
}

func TestQuota_Wait_ThenAllow_SameLimiter(t *testing.T) {
	q := New(WithRate(1), WithBurst(3))
	defer q.Close()

	require.NoError(t, q.WaitN(context.Background(), "k", 2))
	assert.True(t, q.Allow("k"), "WaitN tokens must be visible to Allow on the same limiter")
	assert.False(t, q.Allow("k"), "burst 3: WaitN(2)+Allow(1) exhausts the bucket")
}

func TestQuota_MaxKeys_PinDoesNotLeakCount(t *testing.T) {
	q := New(
		WithMaxKeys(1),
		WithRate(0.0001),
		WithBurst(1),
		WithEvictionTTL(time.Millisecond),
		WithEvictionInterval(time.Hour),
		WithShards(1),
	)
	defer q.Close()

	require.True(t, q.Allow("k"))
	s := q.shardFor("k")
	s.mu.RLock()
	b := s.buckets["k"]
	s.mu.RUnlock()
	require.NotNil(t, b)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- q.Wait(ctx, "k") }()
	testx.Eventually(t, func() bool { return b.pins.Load() > 0 }, time.Second)

	assert.Equal(t, int64(1), q.KeyCount())
	assert.False(t, q.Allow("other"))
	assert.Equal(t, int64(1), q.KeyCount(), "pin must not inflate keyCount")

	cancel()
	require.ErrorIs(t, <-errCh, ErrCancelled)
	assert.Equal(t, int64(1), q.KeyCount())

	time.Sleep(5 * time.Millisecond)
	q.ForceEviction()
	assert.Equal(t, int64(0), q.KeyCount())
	assert.True(t, q.Allow("other"))
}

func TestQuota_BackgroundEviction(t *testing.T) {
	q := New(
		WithEvictionTTL(time.Millisecond),
		WithEvictionInterval(2*time.Millisecond),
	)
	defer q.Close()

	q.Allow("k")
	testx.Eventually(t, func() bool { return q.KeyCount() == 0 }, time.Second)
}

func TestQuota_WaitDelay(t *testing.T) {
	q := New(WithRate(10), WithBurst(5))
	defer q.Close()

	b := q.getOrCreate(q.shardFor("k"), "k")
	require.NotNil(t, b)

	// Full bucket: no deficit, so the floor applies.
	assert.Equal(t, minWaitDelay, q.waitDelay(b, 1))

	// Drain, then request many tokens: a real deficit yields a delay above the
	// floor (5 tokens at 10/s ≈ 500ms).
	require.True(t, b.limiter.AllowN(5))
	d := q.waitDelay(b, 5)
	assert.Greater(t, d, minWaitDelay)

	// Tiny deficit at a very high rate rounds below the floor, so the floor
	// applies again.
	fast := New(WithRate(1e9), WithBurst(2))
	defer fast.Close()
	fb := fast.getOrCreate(fast.shardFor("x"), "x")
	require.True(t, fb.limiter.AllowN(2))
	assert.Equal(t, minWaitDelay, fast.waitDelay(fb, 1))
}

func TestQuota_WaitDelay_UsesLimiterRate(t *testing.T) {
	q := New(WithRate(0.5), WithBurst(5))
	defer q.Close()

	b := q.getOrCreate(q.shardFor("k"), "k")
	require.True(t, b.limiter.AllowN(5))
	d := q.waitDelay(b, 5)
	assert.Greater(t, d, minWaitDelay)
	// 5 tokens at 0.5/s ≈ 10s; allow a little slack for timing math.
	assert.GreaterOrEqual(t, d, 9*time.Second)
	assert.LessOrEqual(t, d, 11*time.Second)
}

func TestQuota_GetOrCreate_ReturnsExisting(t *testing.T) {
	q := New()
	defer q.Close()

	s := q.shardFor("k")
	first := q.getOrCreate(s, "k")
	second := q.getOrCreate(s, "k")
	assert.Same(t, first, second, "second call returns the existing bucket")
	assert.Equal(t, int64(1), q.KeyCount())
}

func TestQuota_Close_Idempotent(t *testing.T) {
	q := New()
	testx.AssertCloseIdempotent(t, q)
	assert.True(t, q.IsClosed())
}

func TestStopTimer_DrainsFiredChannel(t *testing.T) {
	timer := time.NewTimer(time.Millisecond)
	time.Sleep(2 * time.Millisecond)
	stopTimer(timer)
}

func TestStopTimer_StopsBeforeFire(t *testing.T) {
	timer := time.NewTimer(time.Hour)
	require.True(t, timer.Stop())
	stopTimer(timer)
}

func TestQuota_ConcurrentAccess(t *testing.T) {
	q := New(WithRate(1e9), WithBurst(1e9), WithShards(16))
	defer q.Close()

	keys := []string{"a", "b", "c", "d"}
	testx.HammerNoError(t, 50, 200, func() error {
		q.Allow(keys[time.Now().UnixNano()%int64(len(keys))])
		return nil
	})
	assert.LessOrEqual(t, q.KeyCount(), int64(len(keys)))
}

func TestQuota_ConcurrentMaxKeys(t *testing.T) {
	const maxKeys = 10
	q := New(WithMaxKeys(maxKeys), WithShards(8))
	defer q.Close()

	var idx atomic.Int64
	testx.HammerVoid(50, 50, func() {
		k := string(rune('A' + idx.Add(1)%200))
		q.Allow(k)
	})
	assert.LessOrEqual(t, q.KeyCount(), int64(maxKeys), "key cap never exceeded under contention")
}

// --- QuotaController ---

func TestExecute_ControllerSnapshot(t *testing.T) {
	q := New(WithRate(100), WithBurst(50))
	defer q.Close()

	var snapshot struct {
		key    string
		tokens float64
		rate   float64
		burst  int
		waited bool
	}
	_, err := Execute(q, context.Background(), "user-1", func(_ context.Context, qc QuotaController) (int, error) {
		snapshot.key = qc.Key()
		snapshot.tokens = qc.Tokens()
		snapshot.rate = qc.Rate()
		snapshot.burst = qc.Burst()
		snapshot.waited = qc.Waited()
		return 1, nil
	})
	require.NoError(t, err)
	assert.Equal(t, "user-1", snapshot.key)
	assert.InDelta(t, 100.0, snapshot.rate, 0.01)
	assert.Equal(t, 50, snapshot.burst)
	assert.False(t, snapshot.waited)
	assert.GreaterOrEqual(t, snapshot.tokens, 0.0)
}

func TestController_SkipTokenDelegates(t *testing.T) {
	q := New(WithRate(1000), WithBurst(10))
	defer q.Close()

	_, err := Execute(q, context.Background(), "key-a", func(_ context.Context, qc QuotaController) (int, error) {
		qc.SkipToken()
		return 1, nil
	})
	require.NoError(t, err)
}
