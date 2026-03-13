package envx_test

import (
	"fmt"

	"github.com/aasyanov/urx/pkg/envx"
)

func ExampleBind() {
	env := envx.New(
		envx.WithPrefix("APP"),
		envx.WithLookup(envx.MapLookup(map[string]string{
			"APP_PORT": "8080",
		})),
	)

	port := envx.Bind(env, "PORT", 3000)

	if err := env.Validate(); err != nil {
		panic(err)
	}

	fmt.Println(port.Value())
	// Output: 8080
}
