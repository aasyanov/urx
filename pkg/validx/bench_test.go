package validx

import (
	"testing"
	"time"
)

func BenchmarkRequired_Valid(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Required("name", "Alice")
	}
}

func BenchmarkRequired_Invalid(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Required("name", "")
	}
}

func BenchmarkEmail_Valid(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Email("email", "user@example.com")
	}
}

func BenchmarkCollect_3Fields(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Collect(
			Required("name", "Alice"),
			Email("email", "a@b.com"),
			MinLen("pass", "12345678", 8),
		)
	}
}

func BenchmarkMatch(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		Match("code", "ABC-123", `^[A-Z]+-\d+$`)
	}
}

func BenchmarkClamp_InRange(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		v := 50
		Clamp("port", &v, 0, 100)
	}
}

func BenchmarkClamp_Fix(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		v := -1
		Clamp("port", &v, 0, 100)
	}
}

func BenchmarkClampTime_Fix(b *testing.B) {
	now := time.Now()
	min := now.Add(-time.Hour)
	max := now.Add(time.Hour)
	b.ReportAllocs()
	for b.Loop() {
		v := now.Add(-2 * time.Hour)
		ClampTime("ts", &v, min, max)
	}
}

func BenchmarkDefault_Zero(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		v := 0
		Default("retries", &v, 3)
	}
}

func BenchmarkDefaultStr_Empty(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		v := ""
		DefaultStr("env", &v, "production")
	}
}

func BenchmarkDefaultOneOf_Invalid(b *testing.B) {
	allowed := []string{"debug", "info", "warn", "error"}
	b.ReportAllocs()
	for b.Loop() {
		v := "trace"
		DefaultOneOf("level", &v, allowed, "info")
	}
}
