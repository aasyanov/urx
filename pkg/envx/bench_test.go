package envx

import (
	"testing"
	"time"
)

func BenchmarkBind_String(b *testing.B) {
	m := map[string]string{"APP_HOST": "0.0.0.0"}
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithPrefix("APP"), WithLookup(MapLookup(m)))
		Bind(env, "HOST", "localhost")
	}
}

func BenchmarkBind_Int(b *testing.B) {
	m := map[string]string{"PORT": "9090"}
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithLookup(MapLookup(m)))
		Bind(env, "PORT", 8080)
	}
}

func BenchmarkBind_Duration(b *testing.B) {
	m := map[string]string{"TIMEOUT": "30s"}
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithLookup(MapLookup(m)))
		Bind(env, "TIMEOUT", 5*time.Second)
	}
}

func BenchmarkBindRequired_String(b *testing.B) {
	m := map[string]string{"SECRET": "abc123"}
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithLookup(MapLookup(m)))
		BindRequired[string](env, "SECRET")
	}
}

func BenchmarkValidate_5Vars(b *testing.B) {
	m := map[string]string{
		"APP_HOST":    "0.0.0.0",
		"APP_PORT":    "9090",
		"APP_DEBUG":   "true",
		"APP_TIMEOUT": "30s",
		"APP_SECRET":  "key",
	}
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithPrefix("APP"), WithLookup(MapLookup(m)))
		Bind(env, "HOST", "localhost")
		Bind(env, "PORT", 8080)
		Bind(env, "DEBUG", false)
		Bind(env, "TIMEOUT", 5*time.Second)
		BindRequired[string](env, "SECRET")
		env.Validate()
	}
}

func BenchmarkValidate_MissingRequired(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		env := New(WithLookup(MapLookup(nil)))
		BindRequired[string](env, "A")
		BindRequired[string](env, "B")
		BindRequired[string](env, "C")
		env.Validate()
	}
}

func BenchmarkMapLookup(b *testing.B) {
	m := map[string]string{"KEY": "value"}
	fn := MapLookup(m)
	b.ReportAllocs()
	for b.Loop() {
		fn("KEY")
	}
}
