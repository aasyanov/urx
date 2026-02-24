package cronx

import (
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainCron is the [errx] domain for all cronx errors.
const DomainCron = "CRON"

// --- Code constants ---

const (
	// CodeInvalidInput indicates invalid function input.
	CodeInvalidInput = "INVALID_INPUT"

	// CodeAlreadyStarted indicates the scheduler is already running.
	CodeAlreadyStarted = "ALREADY_STARTED"

	// CodeNotStarted indicates the scheduler has not been started.
	CodeNotStarted = "NOT_STARTED"

	// CodeClosed indicates the scheduler has been stopped.
	CodeClosed = "CLOSED"

	// CodeNilFunc indicates a nil job function was provided.
	CodeNilFunc = "NIL_FUNC"

	// CodeJobFailed indicates a job execution failed.
	CodeJobFailed = "JOB_FAILED"

	// CodeShutdownTimeout indicates graceful shutdown timed out.
	CodeShutdownTimeout = "SHUTDOWN_TIMEOUT"
)

// --- Error constructors ---

func errAlreadyStarted() *errx.Error {
	return errx.New(DomainCron, CodeAlreadyStarted, "scheduler already started")
}

func errNotStarted() *errx.Error {
	return errx.New(DomainCron, CodeNotStarted, "scheduler not started")
}

func errClosed() *errx.Error {
	return errx.New(DomainCron, CodeClosed, "scheduler closed")
}

func errNilFunc() *errx.Error {
	return errx.New(DomainCron, CodeNilFunc, "job function must not be nil")
}

func errInvalidInput(msg string) *errx.Error {
	return errx.New(DomainCron, CodeInvalidInput, msg)
}

func errShutdownTimeout(timeout time.Duration) *errx.Error {
	return errx.New(DomainCron, CodeShutdownTimeout, "shutdown timed out",
		errx.WithMeta("timeout", timeout.String()),
	)
}
