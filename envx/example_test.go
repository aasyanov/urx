package envx_test

import (
	"errors"
	"fmt"

	"github.com/aasyanov/urx/envx"
)

// ExampleBind shows typed reads with defaults using an injected lookup so the
// example is deterministic.
func ExampleBind() {
	env := envx.New(
		envx.WithPrefix("APP"),
		envx.WithLookup(envx.MapLookup(map[string]string{
			"APP_PORT": "9090",
		})),
	)

	port := envx.Bind(env, "PORT", 8080)
	host := envx.Bind(env, "HOST", "localhost") // not set → default

	if err := env.Validate(); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s:%d\n", host.Value(), port.Value())
	// Output: localhost:9090
}

// ExampleBindRequired demonstrates the missing-variable report.
func ExampleBindRequired() {
	env := envx.New(envx.WithLookup(envx.MapLookup(map[string]string{})))

	envx.BindRequired[string](env, "SECRET")

	err := env.Validate()
	fmt.Println("missing reported:", errors.Is(err, envx.ErrMissing))
	// Output: missing reported: true
}

// ExampleBindTo overlays environment variables onto a config struct — the
// envx layer of the cfgx → envx → clix pipeline.
func ExampleBindTo() {
	type Config struct {
		Port int
		Host string
	}

	cfg := Config{Port: 8080, Host: "localhost"} // defaults (or from cfgx)

	env := envx.New(envx.WithLookup(envx.MapLookup(map[string]string{
		"PORT": "9090", // HOST not set → keeps "localhost"
	})))
	envx.BindTo(env, "PORT", &cfg.Port)
	envx.BindTo(env, "HOST", &cfg.Host)

	if err := env.Validate(); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%s:%d\n", cfg.Host, cfg.Port)
	// Output: localhost:9090
}

// ExampleBind_list parses a comma-separated value into a []string.
func ExampleBind_list() {
	env := envx.New(envx.WithLookup(envx.MapLookup(map[string]string{
		"ORIGINS": "a.com, b.com ,c.com",
	})))

	origins := envx.Bind(env, "ORIGINS", []string{"localhost"})
	fmt.Println(origins.Value())
	// Output: [a.com b.com c.com]
}
