package quotax

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 80},
		{Name: "Quota", Size: testx.Sizeof[Quota](), Max: 136},
		{Name: "shard", Size: testx.Sizeof[shard](), Max: 32},
		{Name: "bucket", Size: testx.Sizeof[bucket](), Max: 24},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 64},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 24},
	})
}
