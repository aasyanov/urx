package poolx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "WorkerPool", Size: testx.Sizeof[WorkerPool](), Max: 160},
		{Name: "WorkerStats", Size: testx.Sizeof[WorkerStats](), Max: 64},
		{Name: "ObjectPool[int]", Size: testx.Sizeof[ObjectPool[int]](), Max: 80},
		{Name: "ObjectStats", Size: testx.Sizeof[ObjectStats](), Max: 24},
		{Name: "Batch[int]", Size: testx.Sizeof[Batch[int]](), Max: 160},
		{Name: "BatchStats", Size: testx.Sizeof[BatchStats](), Max: 64},
		{Name: "workerConfig", Size: testx.Sizeof[workerConfig](), Max: 32},
		{Name: "batchConfig", Size: testx.Sizeof[batchConfig](), Max: 48},
		{Name: "objectConfig[int]", Size: testx.Sizeof[objectConfig[int]](), Max: 16},
		{Name: "closeSignal", Size: testx.Sizeof[closeSignal](), Max: 16},
	})
}
