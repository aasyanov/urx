package envx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mapEnv(m map[string]string, opts ...Option) *Env {
	all := append([]Option{WithLookup(MapLookup(m))}, opts...)
	return New(all...)
}

func TestBind_AllSupportedTypes(t *testing.T) {
	wantTime := time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC)
	env := mapEnv(map[string]string{
		"STR":   "hello",
		"BOOL":  "true",
		"INT":   "42",
		"INT32": "-7",
		"INT64": "9000000000",
		"UINT":  "12",
		"FLOAT": "3.14",
		"DUR":   "1m30s",
		"AT":    "2025-01-02T15:04:05Z",
		"LIST":  "a, b ,c",
	})

	assert.Equal(t, "hello", Bind(env, "STR", "def").Value())
	assert.True(t, Bind(env, "BOOL", false).Value())
	assert.Equal(t, 42, Bind(env, "INT", 0).Value())
	assert.Equal(t, int32(-7), Bind(env, "INT32", int32(0)).Value())
	assert.Equal(t, int64(9000000000), Bind(env, "INT64", int64(0)).Value())
	assert.Equal(t, uint(12), Bind(env, "UINT", uint(0)).Value())
	assert.InDelta(t, 3.14, Bind(env, "FLOAT", 0.0).Value(), 1e-9)
	assert.Equal(t, 90*time.Second, Bind(env, "DUR", time.Duration(0)).Value())
	assert.True(t, Bind(env, "AT", time.Time{}).Value().Equal(wantTime))
	assert.Equal(t, []string{"a", "b", "c"}, Bind(env, "LIST", []string(nil)).Value())

	require.NoError(t, env.Validate())
}

func TestBind_UsesDefaultWhenAbsent(t *testing.T) {
	env := mapEnv(map[string]string{})
	v := Bind(env, "PORT", 8080)
	assert.Equal(t, 8080, v.Value())
	assert.False(t, v.Found())
	require.NoError(t, env.Validate())
}

func TestBind_InvalidValueReported(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
		bind func(*Env) validator
	}{
		{name: "bad int", key: "N", val: "abc", bind: func(e *Env) validator { return Bind(e, "N", 0) }},
		{name: "bad int32", key: "N", val: "x", bind: func(e *Env) validator { return Bind(e, "N", int32(0)) }},
		{name: "bad int64", key: "N", val: "x", bind: func(e *Env) validator { return Bind(e, "N", int64(0)) }},
		{name: "bad uint", key: "N", val: "-1", bind: func(e *Env) validator { return Bind(e, "N", uint(0)) }},
		{name: "bad float", key: "N", val: "x", bind: func(e *Env) validator { return Bind(e, "N", 0.0) }},
		{name: "bad bool", key: "N", val: "maybe", bind: func(e *Env) validator { return Bind(e, "N", false) }},
		{name: "bad dur", key: "N", val: "soon", bind: func(e *Env) validator { return Bind(e, "N", time.Duration(0)) }},
		{name: "bad time", key: "N", val: "not-a-date", bind: func(e *Env) validator { return Bind(e, "N", time.Time{}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := mapEnv(map[string]string{tt.key: tt.val})
			tt.bind(env)
			require.ErrorIs(t, env.Validate(), ErrInvalid)
		})
	}
}

func TestBind_InvalidKeepsDefault(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "not-a-number"})
	v := Bind(env, "PORT", 8080)
	assert.Equal(t, 8080, v.Value(), "invalid value must not overwrite the default")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBind_UnsupportedType(t *testing.T) {
	env := mapEnv(map[string]string{"X": "1"})
	type custom struct{ A int }
	Bind(env, "X", custom{})
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBindRequired_MissingReported(t *testing.T) {
	env := mapEnv(map[string]string{})
	v := BindRequired[string](env, "SECRET")
	assert.False(t, v.Found())
	require.ErrorIs(t, env.Validate(), ErrMissing)
}

func TestBindRequired_PresentIsValid(t *testing.T) {
	env := mapEnv(map[string]string{"SECRET": "s3cr3t"})
	v := BindRequired[string](env, "SECRET")
	assert.Equal(t, "s3cr3t", v.Value())
	require.NoError(t, env.Validate())
}

func TestBindRequired_InvalidReported(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "not-a-number"})
	BindRequired[int](env, "PORT")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestNew_DefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	assert.Equal(t, defaultPrefix, cfg.prefix)
	assert.NotNil(t, cfg.lookup)
}

func TestBind_LowercaseNameUppercased(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "9090"})
	v := Bind(env, "port", 8080)
	assert.Equal(t, "PORT", v.Key())
	assert.Equal(t, 9090, v.Value())
}

func TestWithPrefix(t *testing.T) {
	tests := []struct {
		name    string
		prefix  string
		wantKey string
	}{
		{name: "simple", prefix: "APP", wantKey: "APP_PORT"},
		{name: "lowercase upcased", prefix: "app", wantKey: "APP_PORT"},
		{name: "trailing underscore trimmed", prefix: "APP_", wantKey: "APP_PORT"},
		{name: "empty prefix", prefix: "", wantKey: "PORT"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := mapEnv(map[string]string{tt.wantKey: "9090"}, WithPrefix(tt.prefix))
			v := Bind(env, "PORT", 0)
			assert.Equal(t, tt.wantKey, v.Key())
			assert.Equal(t, 9090, v.Value())
		})
	}
}

func TestBindTo_OverlaysWhenSet(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "9090"})
	port := 8080
	v := BindTo(env, "PORT", &port)
	assert.Equal(t, 9090, port)
	assert.Equal(t, 9090, v.Value())
	require.NoError(t, env.Validate())
}

func TestBindTo_InvalidKeepsTarget(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "not-a-number"})
	port := 8080
	v := BindTo(env, "PORT", &port)
	assert.Equal(t, 8080, port)
	assert.Equal(t, 8080, v.Value())
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBindTo_PtrAliasesTarget(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "9090"})
	port := 8080
	v := BindTo(env, "PORT", &port)
	require.Same(t, &port, v.Ptr())
	*v.Ptr() = 3000
	assert.Equal(t, 3000, port)
	assert.Equal(t, 3000, v.Value())
}

func TestBindTo_KeepsTargetWhenAbsent(t *testing.T) {
	env := mapEnv(map[string]string{})
	port := 8080
	BindTo(env, "PORT", &port)
	assert.Equal(t, 8080, port, "absent var must leave target unchanged")
}

func TestBindTo_NilTargetPanics(t *testing.T) {
	env := mapEnv(map[string]string{})
	assert.Panics(t, func() { BindTo[int](env, "PORT", nil) })
}

func TestBindRequiredTo_OverlaysAndRequires(t *testing.T) {
	t.Run("present overlays", func(t *testing.T) {
		env := mapEnv(map[string]string{"HOST": "db.prod"})
		host := "localhost"
		BindRequiredTo(env, "HOST", &host)
		assert.Equal(t, "db.prod", host)
		require.NoError(t, env.Validate())
	})

	t.Run("absent reports missing but keeps fallback", func(t *testing.T) {
		env := mapEnv(map[string]string{})
		host := "localhost"
		BindRequiredTo(env, "HOST", &host)
		assert.Equal(t, "localhost", host)
		require.ErrorIs(t, env.Validate(), ErrMissing)
	})

	t.Run("nil target panics", func(t *testing.T) {
		env := mapEnv(map[string]string{})
		assert.Panics(t, func() { BindRequiredTo[string](env, "HOST", nil) })
	})

	t.Run("invalid keeps fallback", func(t *testing.T) {
		env := mapEnv(map[string]string{"PORT": "not-a-number"})
		port := 8080
		v := BindRequiredTo(env, "PORT", &port)
		assert.Equal(t, 8080, port)
		assert.Equal(t, 8080, v.Value())
		require.ErrorIs(t, env.Validate(), ErrInvalid)
	})

	t.Run("ptr aliases target", func(t *testing.T) {
		env := mapEnv(map[string]string{"HOST": "db.prod"})
		host := "localhost"
		v := BindRequiredTo(env, "HOST", &host)
		require.Same(t, &host, v.Ptr())
	})
}

func TestValidate_EmptyEnv(t *testing.T) {
	env := New(WithLookup(MapLookup(map[string]string{})))
	require.NoError(t, env.Validate())
}

func TestValidate_JoinsMultipleErrors(t *testing.T) {
	env := mapEnv(map[string]string{"BAD_INT": "x"})
	Bind(env, "BAD_INT", 0)
	BindRequired[string](env, "MISSING")

	err := env.Validate()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalid)
	assert.ErrorIs(t, err, ErrMissing)
}

func TestVars_ReturnsBoundNamesInOrder(t *testing.T) {
	env := mapEnv(map[string]string{}, WithPrefix("APP"))
	Bind(env, "PORT", 0)
	Bind(env, "HOST", "")
	BindRequired[string](env, "SECRET")
	assert.Equal(t, []string{"APP_PORT", "APP_HOST", "APP_SECRET"}, env.Vars())
}

func TestVar_Accessors(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "9090"})
	v := Bind(env, "PORT", 8080)
	assert.Equal(t, "PORT", v.Key())
	assert.Equal(t, "9090", v.Raw())
	assert.True(t, v.Found())
	assert.Equal(t, 9090, *v.Ptr())

	absent := Bind(env, "MISSING", 1)
	assert.Empty(t, absent.Raw())
	assert.False(t, absent.Found())
}

func TestWithLookup_NilIgnored(t *testing.T) {
	cfg := defaultConfig()
	WithLookup(nil)(&cfg)
	assert.NotNil(t, cfg.lookup)
}

func TestNew_NilOptionIgnored(t *testing.T) {
	assert.NotPanics(t, func() { _ = New(nil) })
	env := New(nil, WithPrefix("APP"), WithLookup(MapLookup(map[string]string{
		"APP_PORT": "9",
	})))
	assert.Equal(t, 9, Bind(env, "PORT", 0).Value())
}

func TestParseList_TrimsAndDropsEmpty(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "simple", raw: "a,b,c", want: []string{"a", "b", "c"}},
		{name: "trims spaces", raw: " a , b ,c ", want: []string{"a", "b", "c"}},
		{name: "drops empty", raw: "a,,b,", want: []string{"a", "b"}},
		{name: "empty string", raw: "", want: []string{}},
		{name: "only commas", raw: ",,,", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseList(tt.raw))
		})
	}
}

func TestMapLookup(t *testing.T) {
	lookup := MapLookup(map[string]string{"K": "v"})
	v, ok := lookup("K")
	assert.True(t, ok)
	assert.Equal(t, "v", v)

	_, ok = lookup("ABSENT")
	assert.False(t, ok)
}

func TestBind_EmptyStringValueIsFound(t *testing.T) {
	env := mapEnv(map[string]string{"OPT": ""})
	v := Bind(env, "OPT", "default")
	assert.True(t, v.Found())
	assert.Equal(t, "", v.Value(), "explicit empty string overrides default")
}

func TestWithFallbackPrefix_PrimaryWins(t *testing.T) {
	env := mapEnv(map[string]string{
		"SMCORE_PORT": "9090",
		"SMP_PORT":    "8080",
	}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	v := Bind(env, "PORT", 0)
	assert.Equal(t, 9090, v.Value())
	assert.Equal(t, "SMCORE_PORT", v.Key())
	require.NoError(t, env.Validate())
}

func TestWithFallbackPrefix_FallbackWhenPrimaryUnset(t *testing.T) {
	env := mapEnv(map[string]string{
		"SMP_PORT": "8080",
	}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	v := Bind(env, "PORT", 0)
	assert.Equal(t, 8080, v.Value())
	assert.Equal(t, "SMP_PORT", v.Key())
	assert.Equal(t, []string{"SMP_PORT"}, env.Vars())
	require.NoError(t, env.Validate())
}

func TestWithFallbackPrefix_BothUnsetBindKeepsDefault(t *testing.T) {
	env := mapEnv(map[string]string{}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	v := Bind(env, "PORT", 8080)
	assert.Equal(t, 8080, v.Value())
	assert.Equal(t, "SMCORE_PORT", v.Key())
	assert.False(t, v.Found())
	require.NoError(t, env.Validate())
}

func TestWithFallbackPrefix_BothUnsetRequiredListsKeys(t *testing.T) {
	env := mapEnv(map[string]string{}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	BindRequired[string](env, "TOKEN")
	err := env.Validate()
	require.ErrorIs(t, err, ErrMissing)
	assert.Contains(t, err.Error(), "SMCORE_TOKEN")
	assert.Contains(t, err.Error(), "SMP_TOKEN")
}

func TestWithFallbackPrefix_EmptyPrimary(t *testing.T) {
	env := mapEnv(map[string]string{
		"SMP_PORT": "7070",
	}, WithPrefix(""), WithFallbackPrefix("SMP"))
	v := Bind(env, "PORT", 0)
	assert.Equal(t, 7070, v.Value())
	assert.Equal(t, "SMP_PORT", v.Key())
}

func TestWithFallbackPrefix_TwoFallbacks(t *testing.T) {
	env := mapEnv(map[string]string{
		"LEGACY_PORT": "6000",
	}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"), WithFallbackPrefix("LEGACY"))
	v := Bind(env, "PORT", 0)
	assert.Equal(t, 6000, v.Value())
	assert.Equal(t, "LEGACY_PORT", v.Key())
}

func TestWithFallbackPrefix_EmptyStringOnPrimaryIsFound(t *testing.T) {
	env := mapEnv(map[string]string{
		"SMCORE_HOST": "",
		"SMP_HOST":    "fallback.local",
	}, WithPrefix("SMCORE"), WithFallbackPrefix("SMP"))
	v := Bind(env, "HOST", "default")
	assert.True(t, v.Found())
	assert.Equal(t, "", v.Value(), "empty primary is Found and must not fall through")
	assert.Equal(t, "SMCORE_HOST", v.Key())
}
