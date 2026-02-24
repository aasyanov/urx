package poolx

import (
	"github.com/aasyanov/urx/pkg/errx"
)

// asErrx extracts an [*errx.Error] from err's chain, returning (nil, false) if none is found.
func asErrx(err error) (*errx.Error, bool) {
	return errx.As(err)
}
