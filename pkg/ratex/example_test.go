package ratex_test

import (
	"fmt"

	"github.com/aasyanov/urx/pkg/ratex"
)

func ExampleLimiter_Allow() {
	rl := ratex.New(ratex.WithRate(1000), ratex.WithBurst(1))

	fmt.Println(rl.Allow())
	// Output: true
}
