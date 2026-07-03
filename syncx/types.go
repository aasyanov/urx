package syncx

import (
	"errors"

	"github.com/aasyanov/urx/panix"
)

// GroupStats is a point-in-time snapshot of [Group] task counters returned by
// [Group.Stats].
type GroupStats struct {
	// Started is the total number of tasks launched via [Group.Go] or a
	// successful [Group.TryGo].
	Started int64 `json:"started"`

	// Succeeded is the number of tasks that returned a nil error.
	Succeeded int64 `json:"succeeded"`

	// Failed is the number of tasks that returned an error or panicked.
	Failed int64 `json:"failed"`

	// Panicked is the number of tasks that panicked (a subset of Failed).
	Panicked int64 `json:"panicked"`
}

// isPanic reports whether err originates from a recovered panic.
func isPanic(err error) bool {
	var pe *panix.PanicError
	return errors.As(err, &pe)
}
