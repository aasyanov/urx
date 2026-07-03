package lrux

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithCapacity(t *testing.T) {
	tests := []struct {
		name string
		opt  Option[string, int]
		want int
	}{
		{name: "default", opt: nil, want: defaultCapacity},
		{name: "custom", opt: WithCapacity[string, int](100), want: 100},
		{name: "zero", opt: WithCapacity[string, int](0), want: 0},
		{name: "negative clamps to zero", opt: WithCapacity[string, int](-5), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option[string, int]
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.capacity)
		})
	}
}

func TestWithTTL(t *testing.T) {
	tests := []struct {
		name string
		opt  Option[string, int]
		want time.Duration
	}{
		{name: "default", opt: nil, want: defaultTTL},
		{name: "custom", opt: WithTTL[string, int](time.Hour), want: time.Hour},
		{name: "zero", opt: WithTTL[string, int](0), want: 0},
		{name: "negative clamps to zero", opt: WithTTL[string, int](-time.Hour), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option[string, int]
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.ttl)
		})
	}
}

func TestWithCleanupInterval(t *testing.T) {
	tests := []struct {
		name string
		opt  Option[string, int]
		want time.Duration
	}{
		{name: "default disabled", opt: nil, want: defaultCleanupInterval},
		{name: "custom", opt: WithCleanupInterval[string, int](time.Minute), want: time.Minute},
		{name: "zero ignored", opt: WithCleanupInterval[string, int](0), want: 0},
		{name: "negative ignored", opt: WithCleanupInterval[string, int](-time.Second), want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option[string, int]
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newConfig(opts)
			assert.Equal(t, tt.want, cfg.cleanupInterval)
		})
	}
}

func TestWithOnEvict(t *testing.T) {
	cfg := newConfig([]Option[string, int]{
		WithOnEvict[string, int](func(string, int, EvictionReason) {}),
	})
	assert.NotNil(t, cfg.onEvict)
}

func TestWithShardCount(t *testing.T) {
	tests := []struct {
		name string
		opt  ShardedOption[string, int]
		want int
	}{
		{name: "default", opt: nil, want: defaultShardCount},
		{name: "custom", opt: WithShardCount[string, int](32), want: 32},
		{name: "zero ignored", opt: WithShardCount[string, int](0), want: defaultShardCount},
		{name: "negative ignored", opt: WithShardCount[string, int](-1), want: defaultShardCount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []ShardedOption[string, int]
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newShardedConfig(opts)
			assert.Equal(t, tt.want, cfg.shardCount)
		})
	}
}

func TestWithShardCapacity(t *testing.T) {
	cfg := newShardedConfig([]ShardedOption[string, int]{
		WithShardCapacity[string, int](50),
	})
	assert.Equal(t, 50, cfg.capacity)

	cfg = newShardedConfig([]ShardedOption[string, int]{
		WithShardCapacity[string, int](-1),
	})
	assert.Equal(t, 0, cfg.capacity)
}

func TestWithShardTTL(t *testing.T) {
	cfg := newShardedConfig([]ShardedOption[string, int]{
		WithShardTTL[string, int](time.Hour),
	})
	assert.Equal(t, time.Hour, cfg.ttl)

	cfg = newShardedConfig([]ShardedOption[string, int]{
		WithShardTTL[string, int](-time.Hour),
	})
	assert.Equal(t, time.Duration(0), cfg.ttl)
}

func TestWithShardOnEvictAndCleanup(t *testing.T) {
	cfg := newShardedConfig([]ShardedOption[string, int]{
		WithShardOnEvict[string, int](func(string, int, EvictionReason) {}),
		WithShardCleanupInterval[string, int](time.Minute),
	})
	assert.NotNil(t, cfg.onEvict)
	assert.Equal(t, time.Minute, cfg.cleanupInterval)
}

func TestComputeOptions(t *testing.T) {
	tests := []struct {
		name             string
		opts             []ComputeOption
		wantTTL          time.Duration
		wantSingleflight bool
	}{
		{name: "empty", opts: nil},
		{name: "ttl", opts: []ComputeOption{WithComputeTTL(time.Minute)}, wantTTL: time.Minute},
		{name: "ttl negative clamps", opts: []ComputeOption{WithComputeTTL(-time.Minute)}, wantTTL: 0},
		{name: "singleflight", opts: []ComputeOption{WithSingleflight()}, wantSingleflight: true},
		{
			name:             "combined",
			opts:             []ComputeOption{WithComputeTTL(time.Second), WithSingleflight()},
			wantTTL:          time.Second,
			wantSingleflight: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newComputeConfig(tt.opts)
			assert.Equal(t, tt.wantTTL, cfg.ttl)
			assert.Equal(t, tt.wantSingleflight, cfg.singleflight)
		})
	}
}

func TestNextPow2(t *testing.T) {
	tests := []struct {
		in   int
		want int
	}{
		{0, 1},
		{1, 1},
		{2, 2},
		{3, 4},
		{16, 16},
		{17, 32},
		{1000, 1024},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, nextPow2(tt.in))
	}
}
