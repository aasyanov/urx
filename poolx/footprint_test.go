package poolx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "WorkerStats", Size: testx.Sizeof[WorkerStats](), Max: 64},
		{Name: "ObjectStats", Size: testx.Sizeof[ObjectStats](), Max: 24},
		{Name: "BatchStats", Size: testx.Sizeof[BatchStats](), Max: 64},
		{Name: "workerConfig", Size: testx.Sizeof[workerConfig](), Max: 16},
		{Name: "batchConfig", Size: testx.Sizeof[batchConfig](), Max: 32},
	})
}
