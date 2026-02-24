package testx

import (
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

func BenchmarkCall_NeverFail(b *testing.B) {
	s := NeverFail()
	b.ReportAllocs()
	for b.Loop() {
		s.Call()
	}
}

func BenchmarkCall_AlwaysFail(b *testing.B) {
	s := AlwaysFail()
	b.ReportAllocs()
	for b.Loop() {
		s.Call()
	}
}

func BenchmarkCall_Pattern(b *testing.B) {
	s := Pattern("SSFS")
	b.ReportAllocs()
	for b.Loop() {
		s.Call()
	}
}

func BenchmarkCall_FailEveryN(b *testing.B) {
	s := FailEvery(5)
	b.ReportAllocs()
	for b.Loop() {
		s.Call()
	}
}

func BenchmarkCall_WithErrorFunc(b *testing.B) {
	s := New(WithFailAlways(), WithErrorFunc(func() *errx.Error {
		return errSimulated("custom")
	}))
	b.ReportAllocs()
	for b.Loop() {
		s.Call()
	}
}
