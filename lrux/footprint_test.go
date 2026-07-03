package lrux

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Cache[string,int]", Size: testx.Sizeof[Cache[string, int]](), Max: 152},
		{Name: "ShardedCache[string,int]", Size: testx.Sizeof[ShardedCache[string, int]](), Max: 48},
		{Name: "node[string,int]", Size: testx.Sizeof[node[string, int]](), Max: 120},
		{Name: "Entry[string,int]", Size: testx.Sizeof[Entry[string, int]](), Max: 96},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 64},
		{Name: "evictEvent[string,int]", Size: testx.Sizeof[evictEvent[string, int]](), Max: 48},
		{Name: "config[string,int]", Size: testx.Sizeof[config[string, int]](), Max: 48},
		{Name: "computeConfig", Size: testx.Sizeof[computeConfig](), Max: 16},
		{Name: "EvictionReason", Size: testx.Sizeof[EvictionReason](), Max: 1},
		{Name: "cleanupTicker", Size: testx.Sizeof[cleanupTicker](), Max: 32},
		{Name: "shardedConfig[string,int]", Size: testx.Sizeof[shardedConfig[string, int]](), Max: 48},
	})
}
