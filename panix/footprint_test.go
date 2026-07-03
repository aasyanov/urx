package panix

import "testing"

func TestFootprint(t *testing.T) {
	assertFootprint(t, []footprintEntry{
		{name: "PanicError", size: sizeofPanicError(), max: 56},
	})
}
