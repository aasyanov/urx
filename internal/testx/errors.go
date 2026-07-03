package testx

import "errors"

// Sentinel errors returned by [Simulator.Call] and [LatencySim.Call].
var (
	// ErrSimulated is the default error returned by [Simulator] on failure.
	ErrSimulated = errors.New("testx: simulated failure")
)
