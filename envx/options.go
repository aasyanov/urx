package envx

import (
	"os"
	"strings"
)

// keySeparator joins a prefix to a variable name: WithPrefix("APP") + "PORT"
// → "APP_PORT".
const keySeparator = "_"

// listSeparator splits a raw value into a []string when binding to a slice
// type: "a,b,c" → []string{"a", "b", "c"}.
const listSeparator = ","

type config struct {
	prefix string
	lookup func(string) (string, bool)
}

func defaultConfig() config {
	return config{
		lookup: os.LookupEnv,
	}
}

// Option configures [New] behavior.
type Option func(*config)

// WithPrefix sets a prefix prepended to all variable names, joined with "_".
// A trailing underscore in prefix is trimmed and the prefix is upper-cased.
// Default: no prefix.
//
// Example: WithPrefix("APP") makes Bind(env, "PORT", 0) read "APP_PORT".
func WithPrefix(prefix string) Option {
	return func(c *config) {
		c.prefix = strings.TrimSuffix(strings.ToUpper(prefix), keySeparator)
	}
}

// WithLookup sets the function used to read environment variables.
// Default: [os.LookupEnv]. A nil function is ignored. Override for testing
// or to read from a custom source.
func WithLookup(fn func(string) (string, bool)) Option {
	return func(c *config) {
		if fn != nil {
			c.lookup = fn
		}
	}
}

// MapLookup returns a lookup function backed by a static map. Useful for
// testing without touching the real environment.
func MapLookup(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}
