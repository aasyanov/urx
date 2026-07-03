package healthx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Checker", Size: testx.Sizeof[Checker](), Max: 80},
		{Name: "config", Size: testx.Sizeof[config](), Max: 8},
		{Name: "Status", Size: testx.Sizeof[Status](), Max: 16},
		{Name: "ComponentStatus", Size: testx.Sizeof[ComponentStatus](), Max: 48},
		{Name: "Report", Size: testx.Sizeof[Report](), Max: 48},
		{Name: "CheckerStats", Size: testx.Sizeof[CheckerStats](), Max: 32},
		{Name: "namedCheck", Size: testx.Sizeof[namedCheck](), Max: 32},
	})
}
