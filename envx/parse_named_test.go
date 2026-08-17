package envx

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type Stamp time.Time

type Level string

type Port int

type codedLevel string

func (l *codedLevel) UnmarshalText(text []byte) error {
	switch string(text) {
	case "debug", "info", "error":
		*l = codedLevel(text)
		return nil
	default:
		return errors.New("unknown level")
	}
}

func TestBind_NamedString(t *testing.T) {
	env := mapEnv(map[string]string{"LEVEL": "debug"})
	v := Bind(env, "LEVEL", Level("info"))
	assert.Equal(t, Level("debug"), v.Value())
	require.NoError(t, env.Validate())
}

func TestBindTo_NamedString(t *testing.T) {
	env := mapEnv(map[string]string{"LEVEL": "error"})
	level := Level("info")
	BindTo(env, "LEVEL", &level)
	assert.Equal(t, Level("error"), level)
	require.NoError(t, env.Validate())
}

func TestBind_NamedStringAbsentKeepsDefault(t *testing.T) {
	env := mapEnv(map[string]string{})
	v := Bind(env, "LEVEL", Level("info"))
	assert.Equal(t, Level("info"), v.Value())
	assert.False(t, v.Found())
	require.NoError(t, env.Validate())
}

func TestBind_NamedIntOverflow(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "999999999999999999999"})
	v := Bind(env, "PORT", Port(8080))
	assert.Equal(t, Port(8080), v.Value(), "overflow must not overwrite the default")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBind_NamedInt(t *testing.T) {
	env := mapEnv(map[string]string{"PORT": "9090"})
	v := Bind(env, "PORT", Port(8080))
	assert.Equal(t, Port(9090), v.Value())
	require.NoError(t, env.Validate())
}

func TestBind_TextUnmarshalerSuccess(t *testing.T) {
	env := mapEnv(map[string]string{"LEVEL": "debug"})
	v := Bind(env, "LEVEL", codedLevel("info"))
	assert.Equal(t, codedLevel("debug"), v.Value())
	require.NoError(t, env.Validate())
}

func TestBind_TextUnmarshalerErrorKeepsDefault(t *testing.T) {
	env := mapEnv(map[string]string{"LEVEL": "silent"})
	v := Bind(env, "LEVEL", codedLevel("info"))
	assert.Equal(t, codedLevel("info"), v.Value(), "unmarshal failure must not overwrite the default")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestParse_NamedStringZeroAllocPathUntouched(t *testing.T) {
	// Exact int still goes through the type-switch; named types use reflect.
	got, diag := parse[int]("42")
	assert.Equal(t, 42, got)
	assert.Empty(t, diag)

	level, diag := parse[Level]("debug")
	assert.Equal(t, Level("debug"), level)
	assert.Empty(t, diag)
}

func TestBind_NamedTimeRFC3339(t *testing.T) {
	want := time.Date(2025, 1, 2, 15, 4, 5, 0, time.UTC)
	env := mapEnv(map[string]string{"AT": "2025-01-02T15:04:05Z"})
	v := Bind(env, "AT", Stamp{})
	assert.True(t, time.Time(v.Value()).Equal(want))
	require.NoError(t, env.Validate())

	dst := Stamp{}
	env2 := mapEnv(map[string]string{"AT": "2025-01-02T15:04:05Z"})
	BindTo(env2, "AT", &dst)
	assert.True(t, time.Time(dst).Equal(want))
	require.NoError(t, env2.Validate())
}

func TestBind_NamedTimeInvalidKeepsDefault(t *testing.T) {
	def := Stamp(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	env := mapEnv(map[string]string{"AT": "not-a-date"})
	v := Bind(env, "AT", def)
	assert.Equal(t, def, v.Value(), "invalid RFC3339 must not overwrite the default")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}
