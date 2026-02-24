package quotax

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainQuota is the [errx] domain for all per-key rate limiting errors.
const DomainQuota = "QUOTA"

const (
	// CodeLimited indicates the request was rejected by the per-key rate limiter.
	CodeLimited = "LIMITED"
	// CodeMaxKeys indicates the maximum number of tracked keys has been reached.
	CodeMaxKeys = "MAX_KEYS"
	// CodeCancelled indicates the wait was cancelled via context.
	CodeCancelled = "CANCELLED"
	// CodeClosed indicates the limiter has been closed.
	CodeClosed = "CLOSED"
)

func errLimited(key string) *errx.Error {
	return errx.New(DomainQuota, CodeLimited, "rate limit exceeded",
		errx.WithRetry(errx.RetrySafe),
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithMeta("key", key),
	)
}

func errMaxKeys(key string) *errx.Error {
	return errx.New(DomainQuota, CodeMaxKeys, "maximum tracked keys reached",
		errx.WithRetry(errx.RetrySafe),
		errx.WithSeverity(errx.SeverityWarn),
		errx.WithMeta("key", key),
	)
}

func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainQuota, CodeCancelled, "wait cancelled")
}

func errClosed() *errx.Error {
	return errx.New(DomainQuota, CodeClosed, "limiter is closed")
}
