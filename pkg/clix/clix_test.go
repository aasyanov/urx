package clix

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain & code constants ---

func TestDomainConstant(t *testing.T) {
	if DomainCLI != "CLI" {
		t.Fatalf("DomainCLI = %q, want %q", DomainCLI, "CLI")
	}
}

func TestCodeConstants(t *testing.T) {
	codes := map[string]string{
		"CodeUnknownFlag":    CodeUnknownFlag,
		"CodeUnknownCommand": CodeUnknownCommand,
		"CodeMissingValue":   CodeMissingValue,
		"CodeInvalidValue":   CodeInvalidValue,
		"CodeRequired":       CodeRequired,
		"CodeEnumViolated":   CodeEnumViolated,
	}
	for name, val := range codes {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

// --- New / basic parsing ---

func TestNew_Defaults(t *testing.T) {
	var port int
	p := New(nil, "app", "test app",
		AddFlag(&port, "port", "p", 8080, "listen port"),
	)
	if err := p.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 8080 {
		t.Fatalf("port = %d, want 8080", port)
	}
}

func TestNew_UnknownFlag(t *testing.T) {
	p := New([]string{"--nope"}, "app", "test")
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownFlag {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownFlag)
	}
}

func TestNew_UnknownShortFlag(t *testing.T) {
	p := New([]string{"-z"}, "app", "test")
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for unknown short flag")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownFlag {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownFlag)
	}
}

// --- AddFlag type tests ---

func TestAddFlag_String(t *testing.T) {
	var host string
	p := New([]string{"--host", "localhost"}, "app", "test",
		AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if host != "localhost" {
		t.Fatalf("host = %q, want %q", host, "localhost")
	}
}

func TestAddFlag_Int(t *testing.T) {
	var port int
	p := New([]string{"--port", "3000"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
}

func TestAddFlag_Bool(t *testing.T) {
	var verbose bool
	p := New([]string{"--verbose"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true")
	}
}

func TestAddFlag_Float64(t *testing.T) {
	var rate float64
	p := New([]string{"--rate", "0.75"}, "app", "test",
		AddFlag(&rate, "rate", "r", 1.0, "rate"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if rate != 0.75 {
		t.Fatalf("rate = %f, want 0.75", rate)
	}
}

func TestAddFlag_Duration(t *testing.T) {
	var timeout time.Duration
	p := New([]string{"--timeout", "5s"}, "app", "test",
		AddFlag(&timeout, "timeout", "t", 10*time.Second, "timeout"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if timeout != 5*time.Second {
		t.Fatalf("timeout = %v, want 5s", timeout)
	}
}

func TestAddFlag_Time(t *testing.T) {
	var deadline time.Time
	def := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	p := New([]string{"--deadline", "2026-06-15T12:00:00Z"}, "app", "test",
		AddFlag(&deadline, "deadline", "d", def, "deadline"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	if !deadline.Equal(want) {
		t.Fatalf("deadline = %v, want %v", deadline, want)
	}
}

func TestAddFlag_ShortFlag(t *testing.T) {
	var port int
	p := New([]string{"-p", "9090"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
}

func TestAddFlag_InvalidValue(t *testing.T) {
	var port int
	p := New([]string{"--port", "abc"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for invalid int value")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeInvalidValue {
		t.Fatalf("code = %q, want %q", ex.Code, CodeInvalidValue)
	}
}

// --- Required ---

func TestAddFlag_Required(t *testing.T) {
	var name string
	p := New(nil, "app", "test",
		AddFlag(&name, "name", "n", "", "user name", Required()),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for missing required flag")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeRequired {
		t.Fatalf("code = %q, want %q", ex.Code, CodeRequired)
	}
}

func TestAddFlag_RequiredProvided(t *testing.T) {
	var name string
	p := New([]string{"--name", "alice"}, "app", "test",
		AddFlag(&name, "name", "n", "", "user name", Required()),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if name != "alice" {
		t.Fatalf("name = %q, want %q", name, "alice")
	}
}

func TestAddFlag_NilTarget_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil target")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "nil target") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	New(nil, "app", "test",
		AddFlag[string](nil, "name", "n", "", "name"),
	)
}

// --- Enum ---

func TestAddFlag_Enum(t *testing.T) {
	var env string
	p := New([]string{"--env", "staging"}, "app", "test",
		AddFlag(&env, "env", "e", "dev", "environment", Enum("dev", "staging", "prod")),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if env != "staging" {
		t.Fatalf("env = %q, want %q", env, "staging")
	}
}

func TestAddFlag_EnumViolated(t *testing.T) {
	var env string
	p := New([]string{"--env", "test"}, "app", "test",
		AddFlag(&env, "env", "e", "dev", "environment", Enum("dev", "staging", "prod")),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for enum violation")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeEnumViolated {
		t.Fatalf("code = %q, want %q", ex.Code, CodeEnumViolated)
	}
}

// --- Subcommands ---

func TestSubCommand_Dispatch(t *testing.T) {
	var ran bool
	p := New([]string{"serve"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error {
				ran = true
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("subcommand action did not run")
	}
}

func TestSubCommand_NestedFlags(t *testing.T) {
	var host string
	p := New([]string{"serve", "--host", "127.0.0.1"}, "app", "test",
		SubCommand("serve", "start server",
			AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
			Run(func(ctx *Context) error { return nil }),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want %q", host, "127.0.0.1")
	}
}

func TestSubCommand_UnknownCommand(t *testing.T) {
	p := New([]string{"nope"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error { return nil }),
		),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownCommand {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownCommand)
	}
}

func TestSubCommand_UnknownCommandWithAction(t *testing.T) {
	var args []string
	p := New([]string{"nope"}, "app", "test",
		SubCommand("serve", "start server"),
		Run(func(ctx *Context) error {
			args = ctx.Args()
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != "nope" {
		t.Fatalf("args = %v, want [nope]", args)
	}
}

// --- Positional arguments ---

func TestArgs_Positional(t *testing.T) {
	var args []string
	p := New([]string{"file1.txt", "file2.txt"}, "app", "test",
		Run(func(ctx *Context) error {
			args = ctx.Args()
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "file1.txt" || args[1] != "file2.txt" {
		t.Fatalf("args = %v, want [file1.txt file2.txt]", args)
	}
}

func TestArgs_DoubleDash(t *testing.T) {
	var port int
	var args []string
	p := New([]string{"--port", "3000", "--", "--not-a-flag", "file.txt"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
		Run(func(ctx *Context) error {
			args = ctx.Args()
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
	if len(args) != 2 || args[0] != "--not-a-flag" || args[1] != "file.txt" {
		t.Fatalf("args = %v, want [--not-a-flag file.txt]", args)
	}
}

func TestArgs_DoubleDashNoFlags(t *testing.T) {
	var args []string
	p := New([]string{"--", "a", "b"}, "app", "test",
		Run(func(ctx *Context) error {
			args = ctx.Args()
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "a" || args[1] != "b" {
		t.Fatalf("args = %v, want [a b]", args)
	}
}

func TestArgs_SubCommandPositional(t *testing.T) {
	var args []string
	p := New([]string{"serve", "extra1", "extra2"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error {
				args = ctx.Args()
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if len(args) != 2 || args[0] != "extra1" || args[1] != "extra2" {
		t.Fatalf("args = %v, want [extra1 extra2]", args)
	}
}

// --- --flag=value syntax ---

func TestFlag_EqualsSyntax(t *testing.T) {
	var port int
	p := New([]string{"--port=9090"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
}

func TestFlag_EqualsSyntaxShort(t *testing.T) {
	var port int
	p := New([]string{"-p=9090"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if port != 9090 {
		t.Fatalf("port = %d, want 9090", port)
	}
}

func TestFlag_BoolEqualsTrue(t *testing.T) {
	var verbose bool
	p := New([]string{"--verbose=true"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true")
	}
}

func TestFlag_BoolEqualsFalse(t *testing.T) {
	var verbose bool
	p := New([]string{"--verbose=false"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", true, "verbose"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if verbose {
		t.Fatal("verbose should be false")
	}
}

// --- Help ---

func TestHelp_ReturnsErrHelp(t *testing.T) {
	p := New([]string{"--help"}, "app", "test")
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", p.Err())
	}
}

func TestHelp_ShortFlag(t *testing.T) {
	p := New([]string{"-h"}, "app", "test")
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", p.Err())
	}
}

func TestHelp_SubCommandHelp(t *testing.T) {
	var host string
	p := New([]string{"serve", "--help"}, "app", "test",
		SubCommand("serve", "start server",
			AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
		),
	)
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", p.Err())
	}
	help := p.Help()
	if help == "" {
		t.Fatal("help string is empty")
	}
	if !containsAll(help, "serve", "--host") {
		t.Fatalf("help missing expected content:\n%s", help)
	}
}

func TestHelp_Format(t *testing.T) {
	var port int
	var verbose bool
	p := New(nil, "myapp", "my tool",
		AddFlag(&port, "port", "p", 8080, "listen port"),
		AddFlag(&verbose, "verbose", "v", false, "enable verbose"),
		SubCommand("serve", "start server"),
	)
	help := p.Help()
	if !containsAll(help, "myapp", "--port", "--verbose", "serve", "USAGE", "FLAGS", "COMMANDS") {
		t.Fatalf("help missing expected content:\n%s", help)
	}
}

func TestHelp_NoShortFlag(t *testing.T) {
	var port int
	p := New(nil, "app", "test",
		AddFlag(&port, "port", "", 8080, "listen port"),
	)
	help := p.Help()
	if !strings.Contains(help, "--port") {
		t.Fatalf("help should contain --port:\n%s", help)
	}
	if strings.Contains(help, ", -") {
		t.Fatalf("help should not contain short flag separator:\n%s", help)
	}
}

// --- Err is *errx.Error ---

func TestErr_IsErrxError(t *testing.T) {
	p := New([]string{"--unknown"}, "app", "test")
	err := p.Err()
	if err == nil {
		t.Fatal("expected error")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Domain != DomainCLI {
		t.Fatalf("domain = %q, want %q", ex.Domain, DomainCLI)
	}
}

// --- Missing value ---

func TestMissingValue(t *testing.T) {
	var port int
	p := New([]string{"--port"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for missing value")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeMissingValue {
		t.Fatalf("code = %q, want %q", ex.Code, CodeMissingValue)
	}
}

// --- Context accessors ---

func TestContext_Args_Nil(t *testing.T) {
	ctx := &Context{}
	if ctx.Args() != nil {
		t.Fatal("expected nil args for empty context")
	}
}

func TestContext_Command(t *testing.T) {
	var got *Command
	p := New([]string{"serve"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error {
				got = ctx.Command()
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Name() != "serve" {
		t.Fatal("expected command name 'serve'")
	}
}

func TestContext_Parser(t *testing.T) {
	var gotParser *Parser
	p := New([]string{"serve"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error {
				gotParser = ctx.Parser()
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if gotParser != p {
		t.Fatal("expected ctx.Parser() to return the same parser")
	}
	help := gotParser.Help()
	if !strings.Contains(help, "serve") {
		t.Fatalf("parser help should mention serve:\n%s", help)
	}
}

// --- Multiple flags ---

func TestMultipleFlags(t *testing.T) {
	var host string
	var port int
	var verbose bool
	p := New([]string{"--host", "localhost", "--port", "3000", "--verbose"}, "app", "test",
		AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
		AddFlag(&port, "port", "p", 8080, "port"),
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if host != "localhost" {
		t.Fatalf("host = %q, want %q", host, "localhost")
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
	if !verbose {
		t.Fatal("verbose should be true")
	}
}

// --- splitFlag ---

func TestSplitFlag(t *testing.T) {
	tests := []struct {
		in       string
		name     string
		value    string
		hasValue bool
	}{
		{"--port=8080", "port", "8080", true},
		{"--verbose", "verbose", "", false},
		{"-p=8080", "p", "8080", true},
		{"-v", "v", "", false},
		{"--key=", "key", "", true},
	}
	for _, tt := range tests {
		name, value, hasValue := splitFlag(tt.in)
		if name != tt.name || value != tt.value || hasValue != tt.hasValue {
			t.Errorf("splitFlag(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.in, name, value, hasValue, tt.name, tt.value, tt.hasValue)
		}
	}
}

// --- Action returns error ---

func TestAction_ReturnsError(t *testing.T) {
	sentinel := fmt.Errorf("boom")
	p := New(nil, "app", "test",
		Run(func(ctx *Context) error { return sentinel }),
	)
	if err := p.Err(); err != nil {
		t.Fatalf("parse should succeed, got %v", err)
	}
	err := p.Run()
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

// --- Nil args with action ---

func TestNilArgs_WithAction(t *testing.T) {
	var ran bool
	p := New(nil, "app", "test",
		Run(func(ctx *Context) error {
			ran = true
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("action should have run")
	}
}

// --- formatDefault ---

func TestFormatDefault_Duration(t *testing.T) {
	got := formatDefault(5 * time.Second)
	if got != "[5s]" {
		t.Fatalf("formatDefault(5s) = %q, want %q", got, "[5s]")
	}
}

func TestFormatDefault_Time(t *testing.T) {
	ts := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	got := formatDefault(ts)
	want := "[2025-06-15T12:00:00Z]"
	if got != want {
		t.Fatalf("formatDefault(time) = %q, want %q", got, want)
	}
}

// --- Unsupported type panics ---

func TestAddFlag_UnsupportedType_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for unsupported type")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "unsupported flag type") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	var b []byte
	AddFlag(&b, "data", "d", nil, "binary data")
}

// --- Enum type mismatch panics ---

func TestAddFlag_EnumTypeMismatch_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for enum type mismatch")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "does not match flag type") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	var port int
	New(nil, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port", Enum("a", "b")),
	)
}

// --- Duplicate flag panics ---

func TestDuplicateFlag_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate flag")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate flag --port") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	var a, b int
	New(nil, "app", "test",
		AddFlag(&a, "port", "p", 8080, "first"),
		AddFlag(&b, "port", "x", 9090, "second"),
	)
}

func TestDuplicateShort_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate short flag")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate short flag -p") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	var a, b int
	New(nil, "app", "test",
		AddFlag(&a, "port", "p", 8080, "first"),
		AddFlag(&b, "listen", "p", 9090, "second"),
	)
}

func TestDuplicateSubCommand_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate subcommand")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate subcommand") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	New(nil, "app", "test",
		SubCommand("serve", "first"),
		SubCommand("serve", "second"),
	)
}

// --- Inherited flags ---

func TestInheritedFlag_FromParent(t *testing.T) {
	var verbose bool
	var host string
	p := New([]string{"serve", "--verbose", "--host", "127.0.0.1"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
		SubCommand("serve", "start server",
			AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
			Run(func(ctx *Context) error { return nil }),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true (inherited from parent)")
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want %q", host, "127.0.0.1")
	}
}

func TestInheritedRequiredFlag_MissingOnSubcommand(t *testing.T) {
	var host string
	p := New([]string{"serve"}, "app", "test",
		AddFlag(&host, "host", "H", "", "bind address", Required()),
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error { return nil }),
		),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected required error from inherited flag")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeRequired {
		t.Fatalf("code = %q, want %q", ex.Code, CodeRequired)
	}
}

func TestInheritedRequiredFlag_ProvidedOnSubcommand(t *testing.T) {
	var host string
	p := New([]string{"serve", "--host", "127.0.0.1"}, "app", "test",
		AddFlag(&host, "host", "H", "", "bind address", Required()),
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error { return nil }),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if host != "127.0.0.1" {
		t.Fatalf("host = %q, want 127.0.0.1", host)
	}
}

func TestInheritedFlag_ShortFromParent(t *testing.T) {
	var verbose bool
	p := New([]string{"serve", "-v"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error { return nil }),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true (inherited short from parent)")
	}
}

func TestInheritedFlag_InHelp(t *testing.T) {
	var verbose bool
	var host string
	p := New([]string{"serve", "--help"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
		SubCommand("serve", "start server",
			AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
		),
	)
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", p.Err())
	}
	help := p.Help()
	if !containsAll(help, "FLAGS:", "--host", "GLOBAL FLAGS:", "--verbose") {
		t.Fatalf("help should show both local and inherited flags:\n%s", help)
	}
}

// --- Grouped short flags (POSIX) ---

func TestGroupedShortFlags_AllBool(t *testing.T) {
	var verbose, debug, quiet bool
	p := New([]string{"-vdq"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		AddFlag(&debug, "debug", "d", false, "debug"),
		AddFlag(&quiet, "quiet", "q", false, "quiet"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose || !debug || !quiet {
		t.Fatalf("expected all true, got v=%v d=%v q=%v", verbose, debug, quiet)
	}
}

func TestGroupedShortFlags_TrailingValue(t *testing.T) {
	var verbose bool
	var port int
	p := New([]string{"-vp", "3000"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true")
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
}

func TestGroupedShortFlags_InlineValue(t *testing.T) {
	var verbose bool
	var port int
	p := New([]string{"-vp3000"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if !verbose {
		t.Fatal("verbose should be true")
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000", port)
	}
}

func TestGroupedShortFlags_UnknownFlag(t *testing.T) {
	var verbose bool
	p := New([]string{"-vz"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for unknown flag in group")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownFlag {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownFlag)
	}
}

func TestGroupedShortFlags_MissingValue(t *testing.T) {
	var verbose bool
	var port int
	p := New([]string{"-vp"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for missing value at end of group")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeMissingValue {
		t.Fatalf("code = %q, want %q", ex.Code, CodeMissingValue)
	}
}

// --- --no-* bool negation ---

func TestNoBoolFlag(t *testing.T) {
	var verbose bool
	p := New([]string{"--no-verbose"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", true, "verbose output"),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if verbose {
		t.Fatal("verbose should be false after --no-verbose")
	}
}

func TestNoBoolFlag_InSubCommand(t *testing.T) {
	var verbose bool
	p := New([]string{"serve", "--no-verbose"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", true, "verbose output"),
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error { return nil }),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if verbose {
		t.Fatal("verbose should be false (--no-verbose via inheritance)")
	}
}

func TestNoBoolFlag_NonBoolIsError(t *testing.T) {
	var port int
	p := New([]string{"--no-port"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for --no-port on non-bool flag")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownFlag {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownFlag)
	}
}

// --- Unknown command error ---

func TestUnknownCommand_Error(t *testing.T) {
	p := New([]string{"migrte"}, "app", "test",
		SubCommand("serve", "start server"),
		SubCommand("migrate", "run migrations"),
	)
	err := p.Err()
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownCommand {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownCommand)
	}
	msg := ex.Message
	if !strings.Contains(msg, "migrte") || !strings.Contains(msg, "serve") {
		t.Fatalf("error message should mention the bad command and available ones: %q", msg)
	}
}

// --- Three levels deep subcommands ---

func TestSubCommand_ThreeLevelsDeep(t *testing.T) {
	var ran bool
	var host string
	p := New([]string{"db", "migrate", "up", "--host", "localhost"}, "app", "test",
		SubCommand("db", "database operations",
			SubCommand("migrate", "migration commands",
				SubCommand("up", "apply migrations",
					AddFlag(&host, "host", "H", "127.0.0.1", "db host"),
					Run(func(ctx *Context) error {
						ran = true
						return nil
					}),
				),
			),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("deeply nested action did not run")
	}
	if host != "localhost" {
		t.Fatalf("host = %q, want %q", host, "localhost")
	}
}

// --- Command.Parent ---

func TestCommand_Parent(t *testing.T) {
	var got *Command
	p := New([]string{"serve"}, "app", "test",
		SubCommand("serve", "start server",
			Run(func(ctx *Context) error {
				got = ctx.Command()
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if got.Parent() == nil {
		t.Fatal("expected serve to have a parent")
	}
	if got.Parent().Name() != "app" {
		t.Fatalf("parent name = %q, want %q", got.Parent().Name(), "app")
	}
}

// --- Parse/Run separation ---

func TestRun_NotCalledOnParseError(t *testing.T) {
	p := New([]string{"--unknown"}, "app", "test",
		Run(func(ctx *Context) error {
			t.Fatal("action should not run on parse error")
			return nil
		}),
	)
	if p.Err() == nil {
		t.Fatal("expected parse error")
	}
	if err := p.Run(); err != nil {
		t.Fatalf("Run should return nil on parse error, got %v", err)
	}
}

func TestRun_NoAction(t *testing.T) {
	p := New(nil, "app", "test")
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatalf("Run with no action should return nil, got %v", err)
	}
}

func TestRun_ParseThenRun(t *testing.T) {
	var port int
	var ran bool
	p := New([]string{"--port", "3000"}, "app", "test",
		AddFlag(&port, "port", "p", 8080, "port"),
		Run(func(ctx *Context) error {
			ran = true
			return nil
		}),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Fatal("action should not run during New")
	}
	if port != 3000 {
		t.Fatalf("port = %d, want 3000 (flags should be parsed in New)", port)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("action should have run after p.Run()")
	}
}

// --- Duplicate Run panics ---

func TestDuplicateRun_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate Run")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate Run") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	first := Run(func(ctx *Context) error { return nil })
	second := Run(func(ctx *Context) error { return nil })
	New(nil, "app", "test", first, second)
}

// --- Aliases ---

func TestAlias_Dispatch(t *testing.T) {
	var ran bool
	p := New([]string{"x"}, "app", "test",
		SubCommand("extract", "extract data",
			Alias("x"),
			Run(func(ctx *Context) error {
				ran = true
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("alias should dispatch to the subcommand action")
	}
}

func TestAlias_PrimaryNameStillWorks(t *testing.T) {
	var ran bool
	p := New([]string{"extract"}, "app", "test",
		SubCommand("extract", "extract data",
			Alias("x"),
			Run(func(ctx *Context) error {
				ran = true
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("primary name should still work with alias registered")
	}
}

func TestAlias_MultipleAliases(t *testing.T) {
	var ran bool
	p := New([]string{"ex"}, "app", "test",
		SubCommand("extract", "extract data",
			Alias("x", "ex"),
			Run(func(ctx *Context) error {
				ran = true
				return nil
			}),
		),
	)
	if err := p.Err(); err != nil {
		t.Fatal(err)
	}
	if err := p.Run(); err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Fatal("second alias should also dispatch")
	}
}

func TestAlias_InHelp(t *testing.T) {
	p := New([]string{"--help"}, "app", "test",
		SubCommand("extract", "extract data",
			Alias("x"),
		),
	)
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", p.Err())
	}
	help := p.Help()
	if !containsAll(help, "extract", "x") {
		t.Fatalf("help should show alias:\n%s", help)
	}
}

func TestAlias_DuplicatePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for duplicate alias")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "duplicate subcommand/alias") {
			t.Fatalf("unexpected panic message: %s", msg)
		}
	}()
	New(nil, "app", "test",
		SubCommand("serve", "first"),
		SubCommand("extract", "second", Alias("serve")),
	)
}

// --- Version ---

func TestVersion_LongFlag(t *testing.T) {
	p := New([]string{"--version"}, "app", "test",
		Version("1.2.3"),
	)
	if !errors.Is(p.Err(), ErrVersion) {
		t.Fatalf("expected ErrVersion, got %v", p.Err())
	}
	if p.Version() != "1.2.3" {
		t.Fatalf("version = %q, want %q", p.Version(), "1.2.3")
	}
}

func TestVersion_ShortFlag(t *testing.T) {
	p := New([]string{"-V"}, "app", "test",
		Version("2.0.0"),
	)
	if !errors.Is(p.Err(), ErrVersion) {
		t.Fatalf("expected ErrVersion, got %v", p.Err())
	}
}

func TestVersion_NotSetIgnored(t *testing.T) {
	p := New([]string{"--version"}, "app", "test")
	err := p.Err()
	if errors.Is(err, ErrVersion) {
		t.Fatal("--version without Version() should not return ErrVersion")
	}
	var ex *errx.Error
	if !errors.As(err, &ex) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if ex.Code != CodeUnknownFlag {
		t.Fatalf("code = %q, want %q", ex.Code, CodeUnknownFlag)
	}
}

func TestVersion_EmptyString(t *testing.T) {
	p := New(nil, "app", "test")
	if p.Version() != "" {
		t.Fatalf("version = %q, want empty", p.Version())
	}
}

// --- Grouped -h triggers help ---

func TestGroupedShortFlags_HelpInGroup(t *testing.T) {
	var verbose bool
	p := New([]string{"-vh"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
	)
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp for -vh, got %v", p.Err())
	}
}

func TestGroupedShortFlags_HelpInGroupSubCommand(t *testing.T) {
	var verbose bool
	p := New([]string{"serve", "-vh"}, "app", "test",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		SubCommand("serve", "start server"),
	)
	if !errors.Is(p.Err(), ErrHelp) {
		t.Fatalf("expected ErrHelp for serve -vh, got %v", p.Err())
	}
	help := p.Help()
	if !strings.Contains(help, "serve") {
		t.Fatalf("help should be for 'serve', got:\n%s", help)
	}
}

// --- Version sets matched ---

func TestVersion_SetsMatched(t *testing.T) {
	p := New([]string{"serve", "--version"}, "app", "test",
		Version("1.0.0"),
		SubCommand("serve", "start server"),
	)
	if !errors.Is(p.Err(), ErrVersion) {
		t.Fatalf("expected ErrVersion, got %v", p.Err())
	}
	help := p.Help()
	if !strings.Contains(help, "serve") {
		t.Fatalf("help after ErrVersion should be for 'serve', got:\n%s", help)
	}
}

// --- helpers ---

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
