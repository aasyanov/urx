package envx

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type namedBool bool
type namedUint uint
type namedFloat float64
type namedDur time.Duration
type namedList []string
type namedInt8 int8

func TestParse_NamedKinds(t *testing.T) {
	env := mapEnv(map[string]string{
		"B":    "true",
		"U":    "9",
		"F":    "1.5",
		"D":    "90",
		"L":    "a,b",
		"I8":   "3",
		"BADB": "maybe",
		"BADU": "-1",
		"BADF": "x",
		"BADD": "soon",
		"OVF":  "99999",
	})

	assert.Equal(t, namedBool(true), Bind(env, "B", namedBool(false)).Value())
	assert.Equal(t, namedUint(9), Bind(env, "U", namedUint(0)).Value())
	assert.InDelta(t, 1.5, float64(Bind(env, "F", namedFloat(0)).Value()), 1e-9)
	assert.Equal(t, namedDur(90), Bind(env, "D", namedDur(0)).Value())
	assert.Equal(t, namedList{"a", "b"}, Bind(env, "L", namedList(nil)).Value())
	assert.Equal(t, namedInt8(3), Bind(env, "I8", namedInt8(0)).Value())
	require.NoError(t, env.Validate())

	bad := mapEnv(map[string]string{
		"BADB": "maybe",
		"BADU": "-1",
		"BADF": "x",
		"BADD": "soon",
		"OVF":  "99999",
	})
	Bind(bad, "BADB", namedBool(true))
	Bind(bad, "BADU", namedUint(1))
	Bind(bad, "BADF", namedFloat(1))
	Bind(bad, "BADD", namedDur(0))
	Bind(bad, "OVF", namedInt8(1))
	require.ErrorIs(t, bad.Validate(), ErrInvalid)
	assert.Equal(t, namedBool(true), Bind(mapEnv(map[string]string{"BADB": "maybe"}), "BADB", namedBool(true)).Value())
}

func TestParseInto_Guards(t *testing.T) {
	assert.NotEmpty(t, parseInto(nil, "1"))
	assert.NotEmpty(t, parseInto((*int)(nil), "1"))
	assert.NotEmpty(t, parseInto(0, "1"))
	assert.NotEmpty(t, parseInto([]int{1}, "1,2"))
}

func TestParse_NamedFloat32AndIntSliceUnsupported(t *testing.T) {
	type namedF32 float32
	env := mapEnv(map[string]string{"F": "1.25", "S": "1,2"})
	assert.Equal(t, namedF32(1.25), Bind(env, "F", namedF32(0)).Value())
	Bind(env, "S", []int(nil))
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBind_KindExtras(t *testing.T) {
	env := mapEnv(map[string]string{
		"I8":  "3",
		"U32": "4",
		"F32": "1.25",
	})
	assert.Equal(t, int8(3), Bind(env, "I8", int8(0)).Value())
	assert.Equal(t, uint32(4), Bind(env, "U32", uint32(0)).Value())
	assert.InDelta(t, 1.25, float64(Bind(env, "F32", float32(0)).Value()), 1e-6)
	require.NoError(t, env.Validate())
}

func TestBind_NamedDurNoLongerParsesDurationString(t *testing.T) {
	env := mapEnv(map[string]string{"D": "5s"})
	v := Bind(env, "D", namedDur(time.Second))
	assert.Equal(t, namedDur(time.Second), v.Value(), "named duration type must not ParseDuration")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestBind_NamedDurUnitlessIntegerStillNanoseconds(t *testing.T) {
	env := mapEnv(map[string]string{"BARE": "90"})
	v := Bind(env, "BARE", namedDur(time.Second))
	assert.Equal(t, namedDur(90), v.Value(), "named int64 accepts a unitless integer as nanoseconds")
	require.NoError(t, env.Validate())
}

func TestBind_NamedInt64RejectsDurationString(t *testing.T) {
	type UserID int64
	env := mapEnv(map[string]string{"ID": "5s"})
	v := Bind(env, "ID", UserID(42))
	assert.Equal(t, UserID(42), v.Value(), "named int64 must not parse duration strings")
	require.ErrorIs(t, env.Validate(), ErrInvalid)
}

func TestWithFallbackPrefix_DuplicatePrimarySkipped(t *testing.T) {
	env := mapEnv(map[string]string{"APP_PORT": "7"}, WithPrefix("APP"), WithFallbackPrefix("APP"))
	v := Bind(env, "PORT", 0)
	assert.Equal(t, 7, v.Value())
	assert.Equal(t, "APP_PORT", v.Key())
}
