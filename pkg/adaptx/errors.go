package adaptx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainAdapt is the [errx] domain for all adaptive limiter errors.
const DomainAdapt = "ADAPT"

const (
	// CodeLimitExceeded indicates the concurrency limit was exceeded.
	CodeLimitExceeded = "LIMIT_EXCEEDED"
	// CodeTimeout indicates the context deadline was exceeded while waiting.
	CodeTimeout = "TIMEOUT"
	// CodeCancelled indicates the context was cancelled while waiting.
	CodeCancelled = "CANCELLED"
	// CodeClosed indicates the limiter has been closed.
	CodeClosed = "CLOSED"
)

func errLimitExceeded() *errx.Error {
	return errx.New(DomainAdapt, CodeLimitExceeded, "concurrency limit exceeded",
		errx.WithRetry(errx.RetrySafe),
		errx.WithSeverity(errx.SeverityWarn),
	)
}

func errTimeout(cause error) *errx.Error {
	return errx.Wrap(cause, DomainAdapt, CodeTimeout, "acquire timed out")
}

func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainAdapt, CodeCancelled, "acquire cancelled")
}

func errClosed() *errx.Error {
	return errx.New(DomainAdapt, CodeClosed, "limiter is closed")
}
