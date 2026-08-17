package envx

import (
	"testing"
	"time"
)

// FuzzParse feeds arbitrary strings through every supported type's parser.
// The oracle: parse must never panic and must always return through its
// (value, diagnostic) contract, no matter how malformed the input.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"", "0", "-1", "9090", "3.14", "true", "false",
		"1m30s", "a,b,c", "  ", "\x00", "1e999",
		"99999999999999999999999999", "0x1F",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parse[string](raw)
		_, _ = parse[bool](raw)
		_, _ = parse[int](raw)
		_, _ = parse[int32](raw)
		_, _ = parse[int64](raw)
		_, _ = parse[uint](raw)
		_, _ = parse[float64](raw)
		_, _ = parse[time.Duration](raw)
		_, _ = parse[time.Time](raw)
		_, _ = parse[[]string](raw)

		type namedStr string
		_, _ = parse[namedStr](raw)
	})
}

// FuzzBindValidate drives the public Bind/Validate path with arbitrary
// key/value pairs through an injected lookup. It must never panic.
func FuzzBindValidate(f *testing.F) {
	f.Add("PORT", "9090")
	f.Add("", "")
	f.Add("X", "not-a-number")
	f.Add("LIST", "a,,b")

	f.Fuzz(func(t *testing.T, key, val string) {
		env := New(WithLookup(MapLookup(map[string]string{key: val})))
		Bind(env, key, 0)
		Bind(env, key, "")
		Bind(env, key, []string(nil))
		_ = env.Validate()
		_ = env.Vars()

		type leaf struct {
			D time.Duration `env:"D"`
			N int64         `env:"N"`
		}
		cfg := leaf{}
		walkEnv := New(WithLookup(MapLookup(map[string]string{"D": val, "N": val})))
		for f := range Walk(&cfg) {
			BindField(walkEnv, f)
		}
		_ = walkEnv.Validate()
	})
}
