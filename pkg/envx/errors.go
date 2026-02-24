package envx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainEnv is the [errx] domain for all environment variable errors.
const DomainEnv = "ENV"

const (
	// CodeMissing indicates a required environment variable is not set.
	CodeMissing = "MISSING"
	// CodeInvalid indicates a variable value could not be parsed.
	CodeInvalid = "INVALID"
)

func errMissing(name string) *errx.Error {
	return errx.New(DomainEnv, CodeMissing, "required environment variable not set",
		errx.WithMeta("var", name),
	)
}

func errInvalid(name, reason string) *errx.Error {
	return errx.New(DomainEnv, CodeInvalid, "invalid environment variable value",
		errx.WithMeta("var", name, "reason", reason),
	)
}
