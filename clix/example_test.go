package clix_test

import (
	"errors"
	"fmt"

	"github.com/aasyanov/urx/clix"
)

// ExampleNew demonstrates the basic flow: define flags, parse, handle the
// help/error sentinels, and run the matched action.
func ExampleNew() {
	var port int
	var verbose bool

	p := clix.New([]string{"serve", "--port", "9090", "-v"}, "myapp", "my awesome tool",
		clix.AddFlag(&verbose, "verbose", "v", false, "enable verbose output"),
		clix.AddFlag(&port, "port", "p", 8080, "listen port"),
		clix.SubCommand("serve", "start the server",
			clix.Run(func(*clix.Context) error {
				fmt.Printf("serving on %d (verbose=%v)\n", port, verbose)
				return nil
			}),
		),
	)

	if errors.Is(p.Err(), clix.ErrHelp) {
		fmt.Println(p.Help())
		return
	}
	if err := p.Err(); err != nil {
		fmt.Println("parse error:", err)
		return
	}
	if err := p.Run(); err != nil {
		fmt.Println("run error:", err)
	}
	// Output: serving on 9090 (verbose=true)
}

// ExampleParser_IsSet shows how IsSet distinguishes an explicitly supplied
// zero value from the flag's default.
func ExampleParser_IsSet() {
	var port int
	p := clix.New([]string{"--port", "0"}, "app", "desc",
		clix.AddFlag(&port, "port", "p", 8080, "listen port"),
	)

	fmt.Printf("port=%d set=%v\n", port, p.IsSet("port"))
	// Output: port=0 set=true
}

// ExampleParser_Reset reuses one parser across multiple argument slices,
// which keeps table-driven tests concise.
func ExampleParser_Reset() {
	var count int
	p := clix.New([]string{"--count", "1"}, "app", "desc",
		clix.AddFlag(&count, "count", "c", 0, "iterations"),
	)
	fmt.Println(count)

	_ = p.Reset([]string{"--count", "5"})
	fmt.Println(count)

	_ = p.Reset([]string{})
	fmt.Println(count)
	// Output:
	// 1
	// 5
	// 0
}

// ExampleParser_Help shows the command legend: full USAGE path, COMMANDS
// with aliases, user FLAGS, and the built-in --help / --version rows.
func ExampleParser_Help() {
	p := clix.New(nil, "app", "demo tool",
		clix.Version("1.0.0"),
		clix.SubCommand("serve", "start the server",
			clix.Alias("s"),
			clix.Run(func(*clix.Context) error { return nil }),
		),
	)
	fmt.Print(p.Help())
	// Output:
	// USAGE: app [flags] [command]
	//
	// demo tool
	//
	// COMMANDS:
	//   serve, s       start the server
	//
	// FLAGS:
	//   --help, -h                        show help
	//   --version, -V                     print version
}

// ExampleWithHelpLabels replaces help chrome without translating command names.
func ExampleWithHelpLabels() {
	p := clix.New(nil, "app", "demo",
		clix.WithHelpLabels(clix.HelpLabels{
			Usage:          "SYNOPSIS",
			Commands:       "SUBCOMMANDS",
			Flags:          "OPTIONS",
			HelpFlag:       "show this help text",
			VersionFlag:    "print version number",
			FlagsMetavar:   "[options]",
			CommandMetavar: "[subcommand]",
		}),
		clix.Version("1.0.0"),
		clix.SubCommand("run", "start the server", clix.Run(func(*clix.Context) error { return nil })),
	)
	fmt.Print(p.Help())
	// Output:
	// SYNOPSIS: app [options] [subcommand]
	//
	// demo
	//
	// SUBCOMMANDS:
	//   run            start the server
	//
	// OPTIONS:
	//   --help, -h                        show this help text
	//   --version, -V                     print version number
}
