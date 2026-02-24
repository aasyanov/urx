package warmupx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainWarmup is the [errx] domain for all warmup errors.
const DomainWarmup = "WARMUP"

const (
	// CodeRejected indicates a request was rejected during warmup.
	CodeRejected = "REJECTED"
)

// errRejected builds a structured rejection error with capacity and progress
// metadata.
func errRejected(capacity, progress float64) *errx.Error {
	return errx.New(DomainWarmup, CodeRejected, "request rejected during warmup",
		errx.WithRetry(errx.RetrySafe),
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithMeta("capacity", capacity, "progress", progress),
	)
}
