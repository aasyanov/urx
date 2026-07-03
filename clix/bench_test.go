package clix

import (
	"testing"
)

func BenchmarkNew_SingleFlag(b *testing.B) {
	args := []string{"--port", "9090"}
	b.ResetTimer()
	for b.Loop() {
		var port int
		_ = New(args, "app", "desc", AddFlag(&port, "port", "p", 8080, "listen port"))
	}
}

func BenchmarkNew_ManyFlags(b *testing.B) {
	args := []string{"--alpha", "1", "--beta", "2", "--gamma", "3", "-v", "--delta", "4.0"}
	b.ResetTimer()
	for b.Loop() {
		var (
			a, bb, g int
			v        bool
			d        float64
		)
		_ = New(args, "app", "desc",
			AddFlag(&a, "alpha", "a", 0, ""),
			AddFlag(&bb, "beta", "b", 0, ""),
			AddFlag(&g, "gamma", "g", 0, ""),
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&d, "delta", "d", 0.0, ""),
		)
	}
}

func BenchmarkNew_Subcommand(b *testing.B) {
	args := []string{"serve", "--port", "9090"}
	b.ResetTimer()
	for b.Loop() {
		var port int
		_ = New(args, "app", "desc",
			AddFlag(&port, "port", "p", 8080, ""),
			SubCommand("serve", "start", Run(noopAction)),
		)
	}
}

func BenchmarkParser_Reset(b *testing.B) {
	var port int
	p := New([]string{"--port", "1"}, "app", "desc", AddFlag(&port, "port", "p", 0, ""))
	args := []string{"--port", "2"}
	b.ResetTimer()
	for b.Loop() {
		_ = p.Reset(args)
	}
}

func BenchmarkParser_Help(b *testing.B) {
	var port int
	var verbose bool
	p := New(nil, "app", "a longer description for the app",
		AddFlag(&port, "port", "p", 8080, "listen port"),
		AddFlag(&verbose, "verbose", "v", false, "enable verbose output"),
		SubCommand("serve", "start the server", Run(noopAction)),
	)
	b.ResetTimer()
	for b.Loop() {
		_ = p.Help()
	}
}

func BenchmarkNew_Parallel(b *testing.B) {
	args := []string{"--port", "9090", "-v"}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var port int
			var v bool
			_ = New(args, "app", "desc",
				AddFlag(&port, "port", "p", 8080, ""),
				AddFlag(&v, "verbose", "v", false, ""),
			)
		}
	})
}
