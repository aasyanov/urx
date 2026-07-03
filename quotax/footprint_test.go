package quotax

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 80},
		{Name: "shard", Size: testx.Sizeof[shard](), Max: 32},
		{Name: "bucket", Size: testx.Sizeof[bucket](), Max: 16},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 64},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 24},
	})
}
