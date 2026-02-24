package fallx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainFallback is the [errx] domain for all fallback errors.
const DomainFallback = "FALLBACK"

const (
	// CodeNoFunc indicates no fallback function was configured for [StrategyFunc].
	CodeNoFunc = "NO_FUNC"
	// CodeFuncFailed indicates the fallback function itself returned an error.
	CodeFuncFailed = "FUNC_FAILED"
	// CodeNoCached indicates no cached result is available for [StrategyCached].
	CodeNoCached = "NO_CACHED"
	// CodeClosed indicates the [Fallback] has been closed.
	CodeClosed = "CLOSED"
)

func errNoFunc() *errx.Error {
	return errx.New(DomainFallback, CodeNoFunc, "no fallback function configured")
}

func errFuncFailed(cause error) *errx.Error {
	return errx.Wrap(cause, DomainFallback, CodeFuncFailed, "fallback function failed",
		errx.WithRetry(errx.RetrySafe),
	)
}

func errNoCached(key string) *errx.Error {
	return errx.New(DomainFallback, CodeNoCached, "no cached result available",
		errx.WithRetry(errx.RetrySafe),
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithMeta("key", key),
	)
}

func errClosed() *errx.Error {
	return errx.New(DomainFallback, CodeClosed, "fallback is closed")
}
