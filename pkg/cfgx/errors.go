package cfgx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// DomainConfig is the [errx] domain for all configuration file errors.
const DomainConfig = "CONFIG"

const (
	// CodeNotFound indicates the config file does not exist.
	CodeNotFound = "NOT_FOUND"
	// CodeReadFailed indicates the file could not be read.
	CodeReadFailed = "READ_FAILED"
	// CodeParseFailed indicates the file content could not be unmarshalled.
	CodeParseFailed = "PARSE_FAILED"
	// CodeWriteFailed indicates the file could not be written.
	CodeWriteFailed = "WRITE_FAILED"
	// CodeUnsupportedFormat indicates the file extension is not recognised.
	CodeUnsupportedFormat = "UNSUPPORTED_FORMAT"
	// CodeInvalidInput indicates Load/Save received an invalid input value.
	CodeInvalidInput = "INVALID_INPUT"
	// CodeValidationFailed indicates Validator returned one or more errors.
	CodeValidationFailed = "VALIDATION_FAILED"
)

func errNotFound(path string) *errx.Error {
	return errx.New(DomainConfig, CodeNotFound, "config file not found",
		errx.WithMeta("path", path),
	)
}

func errReadFailed(path string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainConfig, CodeReadFailed, "could not read config file",
		errx.WithMeta("path", path),
	)
}

func errParseFailed(path string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainConfig, CodeParseFailed, "could not parse config file",
		errx.WithMeta("path", path),
	)
}

func errWriteFailed(path string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainConfig, CodeWriteFailed, "could not write config file",
		errx.WithMeta("path", path),
	)
}

func errUnsupportedFormat(path, ext string) *errx.Error {
	return errx.New(DomainConfig, CodeUnsupportedFormat, "unsupported config file format",
		errx.WithMeta("path", path, "ext", ext),
	)
}

func errInvalidInput(param, reason string) *errx.Error {
	return errx.New(DomainConfig, CodeInvalidInput, "invalid cfgx input",
		errx.WithMeta("param", param, "reason", reason),
	)
}

func errValidationFailed(path string, causes []error) error {
	me := errx.NewMulti()
	for _, cause := range causes {
		if cause == nil {
			continue
		}
		me.Add(errx.Wrap(cause, DomainConfig, CodeValidationFailed, "config validation failed",
			errx.WithMeta("path", path),
		))
	}
	return me.Err()
}
