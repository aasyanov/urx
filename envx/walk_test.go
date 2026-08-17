package envx

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type walkLevel string

type envTagged struct {
	Port    int        `env:"PORT"`
	Host    string     `env:"HOST"`
	Skipped int        // no env tag
	Dash    string     `env:"-"`
	Level   walkLevel  `env:"LEVEL"`
	Origins []string   `env:"ORIGINS"`
	Ports   []int      `env:"PORTS"`
	Child   *walkChild `env:"CHILD"`
	Inline  walkInline `env:",inline"`
}

type walkChild struct {
	Port int `env:"PORT"`
}

type walkInline struct {
	Token string `env:"TOKEN"`
}

type yamlTagged struct {
	Server yamlServer `yaml:"server"`
	Inner  yamlInner  `yaml:",inline"`
}

type yamlServer struct {
	Port int `yaml:"port"`
}

type yamlInner struct {
	Name string `yaml:"app-name"`
}

func collected(dst any, opts ...WalkOption) []Field {
	var out []Field
	for f := range Walk(dst, opts...) {
		out = append(out, f)
	}
	return out
}

func TestWalk_EnvTagAllowlist(t *testing.T) {
	cfg := envTagged{Port: 8080, Skipped: 1, Dash: "x"}
	fields := collected(&cfg)

	keys := make([]string, 0, len(fields))
	paths := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.Key)
		paths = append(paths, f.Path)
		require.NotNil(t, f.Ptr)
	}
	assert.Contains(t, keys, "PORT")
	assert.Contains(t, keys, "HOST")
	assert.Contains(t, keys, "LEVEL")
	assert.Contains(t, keys, "ORIGINS")
	assert.Contains(t, keys, "TOKEN")
	assert.NotContains(t, keys, "SKIPPED")
	assert.NotContains(t, keys, "DASH")
	assert.NotContains(t, keys, "PORTS")
	assert.Contains(t, paths, "Port")
	assert.Contains(t, paths, "Inline.Token")
}

func TestWalk_BindFieldWritesTagged(t *testing.T) {
	cfg := envTagged{Port: 8080, Host: "localhost", Level: "info"}
	env := mapEnv(map[string]string{
		"PORT":  "9090",
		"LEVEL": "debug",
	})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	require.NoError(t, env.Validate())
	assert.Equal(t, 9090, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host)
	assert.Equal(t, walkLevel("debug"), cfg.Level)
}

func TestWalk_KeysFromYAML(t *testing.T) {
	cfg := yamlTagged{Server: yamlServer{Port: 8080}, Inner: yamlInner{Name: "app"}}
	fields := collected(&cfg, KeysFromYAML())

	byPath := map[string]string{}
	for _, f := range fields {
		byPath[f.Path] = f.Key
	}
	assert.Equal(t, "SERVER_PORT", byPath["Server.Port"])
	assert.Equal(t, "APP_NAME", byPath["Inner.Name"])
}

func TestWalk_FallbackPrefix(t *testing.T) {
	cfg := envTagged{Port: 8080}
	env := mapEnv(map[string]string{
		"SMP_PORT": "6000",
	}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	require.NoError(t, env.Validate())
	assert.Equal(t, 6000, cfg.Port)
	assert.Contains(t, env.Vars(), "SMP_PORT")
}

func TestWalk_NilNestedPointerSkipped(t *testing.T) {
	cfg := envTagged{Child: nil}
	assert.NotPanics(t, func() { _ = collected(&cfg) })
	for _, f := range collected(&cfg) {
		assert.NotEqual(t, "CHILD_PORT", f.Key)
	}

	env := mapEnv(map[string]string{"CHILD_PORT": "1"})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	assert.Nil(t, cfg.Child)
}

func TestWalk_StringSliceLeafIntSliceSkipped(t *testing.T) {
	cfg := envTagged{Origins: []string{"a"}}
	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	assert.Contains(t, keys, "ORIGINS")
	assert.NotContains(t, keys, "PORTS")

	env := mapEnv(map[string]string{"ORIGINS": "x, y"})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	assert.Equal(t, []string{"x", "y"}, cfg.Origins)
}

func TestWalk_NamedTypeViaBindField(t *testing.T) {
	cfg := envTagged{Level: "info"}
	env := mapEnv(map[string]string{"LEVEL": "error"})
	for f := range Walk(&cfg) {
		if f.Path == "Level" {
			BindField(env, f)
		}
	}
	require.NoError(t, env.Validate())
	assert.Equal(t, walkLevel("error"), cfg.Level)
}

func TestWalk_BindFieldInvalidKeepsDefault(t *testing.T) {
	cfg := envTagged{Port: 8080}
	env := mapEnv(map[string]string{"PORT": "nope"})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	assert.Equal(t, 8080, cfg.Port)
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestWalk_CycleTerminates(t *testing.T) {
	type node struct {
		Name string `env:"NAME"`
		Next *node  `env:"NEXT"`
	}
	n := &node{Name: "root"}
	n.Next = n
	assert.NotPanics(t, func() { _ = collected(n) })
	fields := collected(n)
	assert.Len(t, fields, 1)
	assert.Equal(t, "NAME", fields[0].Key)
}

func TestWalk_NonPointerPanics(t *testing.T) {
	assert.Panics(t, func() { _ = collected(envTagged{}) })
	assert.Panics(t, func() { _ = collected((*envTagged)(nil)) })
}

func TestBindField_NilPanics(t *testing.T) {
	cfg := envTagged{Port: 1}
	assert.Panics(t, func() { BindField(nil, Field{Key: "PORT", Ptr: &cfg.Port}) })
	assert.Panics(t, func() { BindField(mapEnv(nil), Field{Key: "PORT", Ptr: nil}) })
}

func TestWalk_TimeLeaf(t *testing.T) {
	type timed struct {
		At time.Time `env:"AT"`
	}
	cfg := timed{}
	env := mapEnv(map[string]string{"AT": "2025-01-02T15:04:05Z"})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	require.NoError(t, env.Validate())
	assert.True(t, cfg.At.Equal(time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC)))
}

func TestWalk_KeySourcesAndEarlyStop(t *testing.T) {
	type tagged struct {
		Port int    `json:"port" toml:"port" env:"PORT"`
		Host string `json:"host" toml:"host"`
	}
	cfg := tagged{Port: 1, Host: "h"}
	jsonKeys := []string{}
	for _, f := range collected(&cfg, KeysFromJSON()) {
		jsonKeys = append(jsonKeys, f.Key)
	}
	assert.Contains(t, jsonKeys, "PORT")
	assert.Contains(t, jsonKeys, "HOST")

	tomlKeys := []string{}
	for _, f := range collected(&cfg, KeysFromTOML()) {
		tomlKeys = append(tomlKeys, f.Key)
	}
	assert.Contains(t, tomlKeys, "PORT")

	envKeys := []string{}
	for _, f := range collected(&cfg, KeysFromEnvTag()) {
		envKeys = append(envKeys, f.Key)
	}
	assert.Equal(t, []string{"PORT"}, envKeys)

	stopped := 0
	for range Walk(&cfg, nil) {
		stopped++
		break
	}
	assert.Equal(t, 1, stopped)
}

func TestWalk_PointerLeafAndAnonymous(t *testing.T) {
	type Inner struct {
		N int `json:"n"`
	}
	type wrap struct {
		Inner
		P *int `json:"p"`
	}
	n := 4
	cfg := wrap{Inner: Inner{N: 1}, P: &n}
	fields := collected(&cfg, KeysFromJSON())
	keys := []string{}
	for _, f := range fields {
		keys = append(keys, f.Key)
		require.NotNil(t, f.Ptr)
	}
	assert.Contains(t, keys, "N")
	assert.Contains(t, keys, "P")

	cfg.P = nil
	for _, f := range collected(&cfg, KeysFromJSON()) {
		assert.NotEqual(t, "P", f.Key)
	}
}

func TestWalk_YAMLSkipEmptyTag(t *testing.T) {
	type row struct {
		Port int `yaml:"port"`
		Bare int
	}
	cfg := row{Port: 1, Bare: 2}
	keys := []string{}
	for _, f := range collected(&cfg, KeysFromYAML()) {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{"PORT"}, keys)
}

func TestWalk_InterfaceField(t *testing.T) {
	type box struct {
		Inner any `env:"INNER"`
	}
	type leaf struct {
		N int `env:"N"`
	}
	cfg := box{Inner: &leaf{N: 1}}
	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	assert.Contains(t, keys, "INNER_N")

	cfg.Inner = nil
	assert.NotPanics(t, func() { _ = collected(&cfg) })

	// A non-pointer struct in an interface is not addressable. Walk must
	// not allocate a copy: BindField would write the copy, not cfg.Inner.
	cfg.Inner = leaf{N: 2}
	assert.Empty(t, collected(&cfg), "non-pointer interface value yields no leaves")
}

func TestWalk_NestedEarlyStopAndDashName(t *testing.T) {
	type inner struct {
		A int `env:"A"`
		B int `env:"B"`
	}
	type wrap struct {
		Inner inner `env:"INNER"`
		Dash  int   `env:"-,inline"`
	}
	cfg := wrap{Inner: inner{A: 1, B: 2}}
	n := 0
	for range Walk(&cfg) {
		n++
		break
	}
	assert.Equal(t, 1, n)

	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	assert.NotContains(t, keys, "DASH")
}

func TestWalk_YAMLAnonymous(t *testing.T) {
	type Inner struct {
		Port int `yaml:"port"`
	}
	type wrap struct {
		Inner
	}
	cfg := wrap{Inner: Inner{Port: 1}}
	keys := []string{}
	for _, f := range collected(&cfg, KeysFromYAML()) {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{"PORT"}, keys)
}

func TestDerefWalk_Invalid(t *testing.T) {
	_, ok := derefWalk(reflect.Value{}, map[uintptr]bool{})
	assert.False(t, ok)
}

func TestWalk_InterfaceHoldsScalar(t *testing.T) {
	type box struct {
		Inner any `env:"INNER"`
	}
	cfg := box{Inner: 3}
	assert.NotPanics(t, func() { _ = collected(&cfg) })

	type inner struct {
		A int `env:"A"`
		B int `env:"B"`
	}
	cfg.Inner = &inner{A: 1, B: 2}
	n := 0
	for range Walk(&cfg) {
		n++
		break
	}
	assert.Equal(t, 1, n)
}

func TestWalk_JSONEmptyTagUsesFieldName(t *testing.T) {
	type row struct {
		Bare int
	}
	cfg := row{Bare: 1}
	keys := []string{}
	for _, f := range collected(&cfg, KeysFromJSON()) {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{"BARE"}, keys)
}

func TestJoinEnvKey(t *testing.T) {
	assert.Equal(t, "PARENT", joinEnvKey("PARENT", ""))
	assert.Equal(t, "CHILD", joinEnvKey("", "CHILD"))
	assert.Equal(t, "PARENT_CHILD", joinEnvKey("PARENT", "CHILD"))
}

func TestWalk_UnexportedSkipped(t *testing.T) {
	type row struct {
		Port int `env:"PORT"`
		hide int `env:"HIDE"`
	}
	cfg := row{Port: 1, hide: 2}
	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{"PORT"}, keys)
}

func TestWalk_NamedInt64RejectsDurationString(t *testing.T) {
	type UserID int64
	type row struct {
		ID UserID `env:"ID"`
	}
	cfg := row{ID: 42}
	env := mapEnv(map[string]string{"ID": "5s"})
	for f := range Walk(&cfg) {
		BindField(env, f)
	}
	assert.Equal(t, UserID(42), cfg.ID, "named int64 must not parse duration strings")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestWalk_NamedTimeViaBindField(t *testing.T) {
	type Stamp time.Time
	type timed struct {
		At Stamp `env:"AT"`
	}
	cfg := timed{}
	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	// *Stamp does not implement encoding.TextUnmarshaler, so Walk treats
	// named time.Time as a struct and finds no exported leaves. Bind/BindTo
	// still parse RFC3339; Walk cannot overlay this field.
	assert.Empty(t, keys)
	assert.NotContains(t, keys, "AT")
}

func TestWalk_DurationMatchesBind(t *testing.T) {
	type timed struct {
		D time.Duration `env:"D"`
		N int64         `env:"N"`
	}

	t.Run("bare number invalid on Duration, duration string invalid on int64", func(t *testing.T) {
		cfg := timed{D: time.Second, N: 7}
		env := mapEnv(map[string]string{"D": "90", "N": "1s"})
		for f := range Walk(&cfg) {
			BindField(env, f)
		}
		assert.Equal(t, time.Second, cfg.D, "Duration must not accept unitless 90 as 90ns")
		assert.Equal(t, int64(7), cfg.N, "int64 must not accept duration strings")
		require.ErrorIs(t, env.Validate(), ErrInvalid)
	})

	t.Run("ParseDuration and ParseInt succeed on the matching types", func(t *testing.T) {
		cfg := timed{}
		env := mapEnv(map[string]string{"D": "1m30s", "N": "7"})
		for f := range Walk(&cfg) {
			BindField(env, f)
		}
		require.NoError(t, env.Validate())
		assert.Equal(t, 90*time.Second, cfg.D)
		assert.Equal(t, int64(7), cfg.N)
	})
}

func TestWalk_AliasedPointerSkippedOnce(t *testing.T) {
	type leaf struct {
		N int `env:"N"`
	}
	type wrap struct {
		A *leaf `env:"A"`
		B *leaf `env:"B"`
	}
	shared := &leaf{N: 1}
	cfg := wrap{A: shared, B: shared}
	keys := []string{}
	for _, f := range collected(&cfg) {
		keys = append(keys, f.Key)
	}
	assert.Equal(t, []string{"A_N"}, keys)
}
