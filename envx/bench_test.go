package envx

import (
	"testing"
	"time"
)

func benchEnv() *Env {
	return New(WithLookup(MapLookup(map[string]string{
		"PORT": "9090",
		"HOST": "db.local",
		"DUR":  "1m30s",
		"AT":   "2025-01-02T15:04:05Z",
		"LIST": "a,b,c,d,e",
	})))
}

func resetBound(env *Env) {
	env.vars = env.vars[:0]
}

func BenchmarkBind_Int(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "PORT", 0)
	}
}

func BenchmarkBind_String(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "HOST", "")
	}
}

func BenchmarkBind_Duration(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "DUR", time.Duration(0))
	}
}

func BenchmarkBind_List(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "LIST", []string(nil))
	}
}

func BenchmarkBind_Absent(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "NOPE", 42)
	}
}

func BenchmarkBind_Time(b *testing.B) {
	env := benchEnv()
	b.ResetTimer()
	for b.Loop() {
		resetBound(env)
		_ = Bind(env, "AT", time.Time{})
	}
}

func BenchmarkValidate(b *testing.B) {
	env := benchEnv()
	Bind(env, "PORT", 0)
	Bind(env, "HOST", "")
	BindRequired[string](env, "MISSING_OK_FOR_BENCH")
	b.ResetTimer()
	for b.Loop() {
		_ = env.Validate()
	}
}

func BenchmarkParse_Int(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		_, _ = parse[int]("9090")
	}
}

func BenchmarkParse_Time(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		_, _ = parse[time.Time]("2025-01-02T15:04:05Z")
	}
}
