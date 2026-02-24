package env2x

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainEnv2 is the [errx] domain for all reflection-based environment
// overlay errors.
const DomainEnv2 = "ENV2"

const (
	// CodeParseFailed indicates an env variable value could not be
	// converted to the target field's type.
	CodeParseFailed = "PARSE_FAILED"
	// CodeNotSettable indicates a struct field is unexported or otherwise
	// not addressable by reflection.
	CodeNotSettable = "NOT_SETTABLE"
	// CodeUnsupportedType indicates the field type is not one of the
	// supported scalar types.
	CodeUnsupportedType = "UNSUPPORTED_TYPE"
	// CodeInvalidInput indicates Overlay received an invalid target value.
	CodeInvalidInput = "INVALID_INPUT"
)

func errInvalidInput(reason string) *errx.Error {
	return errx.New(DomainEnv2, CodeInvalidInput, "invalid env2x input",
		errx.WithMeta("reason", reason),
	)
}

func errParseFailed(envVar, field, reason string) *errx.Error {
	return errx.New(DomainEnv2, CodeParseFailed, "failed to parse environment variable",
		errx.WithMeta("var", envVar, "field", field, "reason", reason),
	)
}

func errNotSettable(envVar, field string) *errx.Error {
	return errx.New(DomainEnv2, CodeNotSettable, "struct field is not settable",
		errx.WithMeta("var", envVar, "field", field),
	)
}

func errUnsupportedType(envVar, field, typeName string) *errx.Error {
	return errx.New(DomainEnv2, CodeUnsupportedType, "unsupported field type for env binding",
		errx.WithMeta("var", envVar, "field", field, "type", typeName),
	)
}
