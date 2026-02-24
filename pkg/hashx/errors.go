package hashx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainHash is the [errx] domain for all password hashing errors.
const DomainHash = "HASH"

const (
	// CodeEmptyPassword indicates an empty password was provided.
	CodeEmptyPassword = "EMPTY_PASSWORD"
	// CodeMismatch indicates the password does not match the hash.
	CodeMismatch = "MISMATCH"
	// CodeInvalidHash indicates the stored hash string is malformed.
	CodeInvalidHash = "INVALID_HASH"
	// CodeInternal indicates an internal cryptographic error.
	CodeInternal = "INTERNAL"
	// CodeCancelled indicates the operation was cancelled via context.
	CodeCancelled = "CANCELLED"
)

func errEmptyPassword() *errx.Error {
	return errx.New(DomainHash, CodeEmptyPassword, "password must not be empty")
}

func errMismatch() *errx.Error {
	return errx.New(DomainHash, CodeMismatch, "password does not match",
		errx.WithSeverity(errx.SeverityWarn),
	)
}

func errInvalidHash(cause error) *errx.Error {
	return errx.Wrap(cause, DomainHash, CodeInvalidHash, "invalid hash format")
}

func errInternal(cause error) *errx.Error {
	return errx.Wrap(cause, DomainHash, CodeInternal, "hashing operation failed")
}

func errCancelled(cause error) *errx.Error {
	return errx.Wrap(cause, DomainHash, CodeCancelled, "hashing cancelled")
}
