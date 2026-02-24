package clix

import "testing"

func BenchmarkNew_Simple(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var port int
		var host string
		var verbose bool
		p := New([]string{"--port", "3000", "--host", "localhost", "--verbose"}, "app", "bench",
			AddFlag(&port, "port", "p", 8080, "port"),
			AddFlag(&host, "host", "H", "0.0.0.0", "host"),
			AddFlag(&verbose, "verbose", "v", false, "verbose"),
		)
		if p.Err() != nil {
			b.Fatal(p.Err())
		}
	}
}

func BenchmarkNew_SubCommand(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var host string
		var port int
		var globalVerbose bool
		p := New([]string{"serve", "--host", "127.0.0.1", "--port", "9090"}, "app", "bench",
			AddFlag(&globalVerbose, "verbose", "v", false, "global verbose"),
			SubCommand("serve", "start server",
				AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
				AddFlag(&port, "port", "p", 8080, "port"),
				Run(func(ctx *Context) error { return nil }),
			),
		)
		if p.Err() != nil {
			b.Fatal(p.Err())
		}
	}
}

func BenchmarkHelp(b *testing.B) {
	b.ReportAllocs()
	var port int
	var verbose bool
	p := New(nil, "myapp", "my tool",
		AddFlag(&port, "port", "p", 8080, "listen port"),
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
		SubCommand("serve", "start server"),
		SubCommand("migrate", "run migrations"),
	)
	b.ResetTimer()
	for b.Loop() {
		_ = p.Help()
	}
}

func BenchmarkAddFlag_Generic(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var port int
		cmd := newCommand("app", "bench")
		opt := AddFlag(&port, "port", "p", 8080, "port")
		opt(cmd)
	}
}
