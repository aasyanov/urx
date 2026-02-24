package dicx

import (
	"context"
	"testing"
)

// --- Benchmark types ---

type benchConfig struct{ DSN string }
type benchLogger struct{ Prefix string }
type benchDB struct{ Cfg *benchConfig }
type benchSvc struct {
	DB  *benchDB
	Log *benchLogger
}

func newBenchConfig() *benchConfig          { return &benchConfig{DSN: "pg://localhost"} }
func newBenchLogger() *benchLogger          { return &benchLogger{Prefix: "app"} }
func newBenchDB(cfg *benchConfig) *benchDB  { return &benchDB{Cfg: cfg} }
func newBenchSvc(db *benchDB, log *benchLogger) *benchSvc {
	return &benchSvc{DB: db, Log: log}
}

type bL0 struct{}
type bL1 struct{ *bL0 }
type bL2 struct{ *bL1 }
type bL3 struct{ *bL2 }
type bL4 struct{ *bL3 }

func newBL0() *bL0          { return &bL0{} }
func newBL1(d *bL0) *bL1    { return &bL1{d} }
func newBL2(d *bL1) *bL2    { return &bL2{d} }
func newBL3(d *bL2) *bL3    { return &bL3{d} }
func newBL4(d *bL3) *bL4    { return &bL4{d} }

// --- BenchmarkProvide ---

func BenchmarkProvide(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c := New()
		_ = c.Provide(newBenchConfig)
	}
}

// --- BenchmarkResolve_Singleton_Cached (hot path) ---

func BenchmarkResolve_Singleton_Cached(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig)
	_, _ = Resolve[*benchConfig](c) // warm the cache

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Resolve[*benchConfig](c)
	}
}

// --- BenchmarkResolve_Singleton_Cold (first resolve) ---

func BenchmarkResolve_Singleton_Cold(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c := New()
		_ = c.Provide(newBenchConfig)
		_ = c.Provide(newBenchDB)
		_, _ = Resolve[*benchDB](c)
	}
}

// --- BenchmarkResolve_Transient ---

func BenchmarkResolve_Transient(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig, WithLifetime(Transient))

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Resolve[*benchConfig](c)
	}
}

// --- BenchmarkResolve_DeepChain (5 levels) ---

func BenchmarkResolve_DeepChain(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c := New()
		_ = c.Provide(newBL0)
		_ = c.Provide(newBL1)
		_ = c.Provide(newBL2)
		_ = c.Provide(newBL3)
		_ = c.Provide(newBL4)
		_, _ = Resolve[*bL4](c)
	}
}

// --- BenchmarkStart ---

func BenchmarkStart(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		c := New()
		_ = c.Provide(newBenchConfig)
		_ = c.Provide(newBenchLogger)
		_ = c.Provide(newBenchDB)
		_ = c.Provide(newBenchSvc)
		_ = c.Start(context.Background())
	}
}

// --- BenchmarkConcurrentResolve ---

func BenchmarkConcurrentResolve(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig)
	_ = c.Provide(newBenchLogger)
	_ = c.Provide(newBenchDB)
	_ = c.Provide(newBenchSvc)
	_, _ = Resolve[*benchSvc](c) // warm

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Resolve[*benchSvc](c)
		}
	})
}

// --- BenchmarkResolve_WithDeps (2 deps, cached) ---

func BenchmarkResolve_WithDeps_Cached(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig)
	_ = c.Provide(newBenchLogger)
	_ = c.Provide(newBenchDB)
	_ = c.Provide(newBenchSvc)
	_, _ = Resolve[*benchSvc](c) // warm

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, _ = Resolve[*benchSvc](c)
	}
}

// --- BenchmarkMustResolve_Cached ---

func BenchmarkMustResolve_Cached(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig)
	_ = MustResolve[*benchConfig](c) // warm

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_ = MustResolve[*benchConfig](c)
	}
}

// --- BenchmarkConcurrentResolve_Contention (high goroutine count) ---

func BenchmarkConcurrentResolve_Contention(b *testing.B) {
	c := New()
	_ = c.Provide(newBenchConfig)
	_, _ = Resolve[*benchConfig](c) // warm

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Resolve[*benchConfig](c)
		}
	})
}
