package clix

import (
	"strings"
	"testing"
	"time"
)

// FuzzParse feeds arbitrary whitespace-separated argument strings through a
// representative command tree. The oracle: New must never panic and must
// always return through Err — no matter how malformed the input.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"--port 8080",
		"-vp3000",
		"--no-verbose",
		"-- --raw file.txt",
		"serve --port=1 -v",
		"--env prod",
		"--dur 1m30s",
		"-",
		"--",
		"---",
		"--port",
		"--port=",
		"=value",
		"--=x",
		strings.Repeat("-a", 64),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		args := strings.Fields(raw)

		var (
			port    int
			verbose bool
			env     string
			dur     time.Duration
		)
		p := New(args, "app", "desc",
			AddFlag(&port, "port", "p", 8080, "listen port"),
			AddFlag(&verbose, "verbose", "v", false, "verbose"),
			AddFlag(&env, "env", "e", "dev", "", Enum("dev", "staging", "prod")),
			AddFlag(&dur, "dur", "d", time.Duration(0), "duration"),
			SubCommand("serve", "start", Run(noopAction)),
		)

		// Err must be safe to call and Help must never panic regardless of
		// the parse outcome.
		_ = p.Err()
		_ = p.Help()

		// Run must be safe even after a parse error.
		_ = p.Run()
	})
}
