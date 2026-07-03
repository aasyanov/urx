package clix

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_ParsesLongShortAndInlineFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "long with space", args: []string{"--port", "9090"}, want: 9090},
		{name: "short with space", args: []string{"-p", "9090"}, want: 9090},
		{name: "long inline", args: []string{"--port=9090"}, want: 9090},
		{name: "short inline", args: []string{"-p=9090"}, want: 9090},
		{name: "default when absent", args: []string{}, want: 8080},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var port int
			p := New(tt.args, "app", "desc", AddFlag(&port, "port", "p", 8080, "listen port"))
			require.NoError(t, p.Err())
			assert.Equal(t, tt.want, port)
		})
	}
}

func TestNew_BoolFlagFormsAndNegation(t *testing.T) {
	tests := []struct {
		name string
		def  bool
		args []string
		want bool
	}{
		{name: "presence sets true", def: false, args: []string{"--verbose"}, want: true},
		{name: "short presence", def: false, args: []string{"-v"}, want: true},
		{name: "inline true", def: false, args: []string{"--verbose=true"}, want: true},
		{name: "inline false", def: true, args: []string{"--verbose=false"}, want: false},
		{name: "no- negation", def: true, args: []string{"--no-verbose"}, want: false},
		{name: "absent keeps default", def: true, args: []string{}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var verbose bool
			p := New(tt.args, "app", "desc", AddFlag(&verbose, "verbose", "v", tt.def, "verbose"))
			require.NoError(t, p.Err())
			assert.Equal(t, tt.want, verbose)
		})
	}
}

func TestNew_GroupedShortFlags(t *testing.T) {
	t.Run("all bool group", func(t *testing.T) {
		var a, b, c bool
		p := New([]string{"-abc"}, "app", "desc",
			AddFlag(&a, "alpha", "a", false, ""),
			AddFlag(&b, "beta", "b", false, ""),
			AddFlag(&c, "gamma", "c", false, ""),
		)
		require.NoError(t, p.Err())
		assert.True(t, a)
		assert.True(t, b)
		assert.True(t, c)
	})

	t.Run("trailing non-bool consumes next arg", func(t *testing.T) {
		var v bool
		var port int
		p := New([]string{"-vp", "3000"}, "app", "desc",
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&port, "port", "p", 0, ""),
		)
		require.NoError(t, p.Err())
		assert.True(t, v)
		assert.Equal(t, 3000, port)
	})

	t.Run("trailing non-bool consumes rest of group", func(t *testing.T) {
		var v bool
		var port int
		p := New([]string{"-vp3000"}, "app", "desc",
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&port, "port", "p", 0, ""),
		)
		require.NoError(t, p.Err())
		assert.True(t, v)
		assert.Equal(t, 3000, port)
	})

	t.Run("unknown flag in group", func(t *testing.T) {
		var v bool
		p := New([]string{"-vx"}, "app", "desc", AddFlag(&v, "verbose", "v", false, ""))
		require.ErrorIs(t, p.Err(), ErrUnknownFlag)
	})

	t.Run("missing value for trailing group flag", func(t *testing.T) {
		var v bool
		var port int
		p := New([]string{"-vp"}, "app", "desc",
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&port, "port", "p", 0, ""),
		)
		require.ErrorIs(t, p.Err(), ErrMissingValue)
	})
}

func TestNew_DoubleDashTerminator(t *testing.T) {
	var v bool
	p := New([]string{"--verbose", "--", "--not-a-flag", "file.txt"}, "app", "desc",
		AddFlag(&v, "verbose", "v", false, ""),
	)
	require.NoError(t, p.Err())
	assert.True(t, v)
	assert.Equal(t, []string{"--not-a-flag", "file.txt"}, p.Command().Args())
}

func TestNew_AllSupportedTypes(t *testing.T) {
	var (
		s string
		i int
		b bool
		f float64
		d time.Duration
		ts time.Time
	)
	args := []string{
		"--str", "hello",
		"--int", "42",
		"--bool",
		"--float", "3.14",
		"--dur", "1m30s",
		"--time", "2025-01-02T15:04:05Z",
	}
	p := New(args, "app", "desc",
		AddFlag(&s, "str", "", "", ""),
		AddFlag(&i, "int", "", 0, ""),
		AddFlag(&b, "bool", "", false, ""),
		AddFlag(&f, "float", "", 0.0, ""),
		AddFlag(&d, "dur", "", time.Duration(0), ""),
		AddFlag(&ts, "time", "", time.Time{}, ""),
	)
	require.NoError(t, p.Err())
	assert.Equal(t, "hello", s)
	assert.Equal(t, 42, i)
	assert.True(t, b)
	assert.InDelta(t, 3.14, f, 1e-9)
	assert.Equal(t, 90*time.Second, d)
	assert.Equal(t, 2025, ts.Year())
}

func TestNew_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		build   func() *Parser
		wantErr error
	}{
		{
			name: "unknown long flag",
			build: func() *Parser {
				return New([]string{"--nope"}, "app", "desc")
			},
			wantErr: ErrUnknownFlag,
		},
		{
			name: "unknown short flag",
			build: func() *Parser {
				return New([]string{"-z"}, "app", "desc")
			},
			wantErr: ErrUnknownFlag,
		},
		{
			name: "missing value",
			build: func() *Parser {
				var port int
				return New([]string{"--port"}, "app", "desc", AddFlag(&port, "port", "p", 0, ""))
			},
			wantErr: ErrMissingValue,
		},
		{
			name: "invalid value",
			build: func() *Parser {
				var port int
				return New([]string{"--port", "abc"}, "app", "desc", AddFlag(&port, "port", "p", 0, ""))
			},
			wantErr: ErrInvalidValue,
		},
		{
			name: "required not provided",
			build: func() *Parser {
				var host string
				return New([]string{}, "app", "desc", AddFlag(&host, "host", "", "", "", Required()))
			},
			wantErr: ErrRequired,
		},
		{
			name: "enum violated",
			build: func() *Parser {
				var env string
				return New([]string{"--env", "qa"}, "app", "desc",
					AddFlag(&env, "env", "e", "dev", "", Enum("dev", "prod")))
			},
			wantErr: ErrEnumViolated,
		},
		{
			name: "unknown command",
			build: func() *Parser {
				return New([]string{"bogus"}, "app", "desc", SubCommand("serve", "s", Run(noopAction)))
			},
			wantErr: ErrUnknownCommand,
		},
		{
			name: "help long",
			build: func() *Parser {
				return New([]string{"--help"}, "app", "desc")
			},
			wantErr: ErrHelp,
		},
		{
			name: "help short",
			build: func() *Parser {
				return New([]string{"-h"}, "app", "desc")
			},
			wantErr: ErrHelp,
		},
		{
			name: "version",
			build: func() *Parser {
				return New([]string{"--version"}, "app", "desc", Version("1.0.0"))
			},
			wantErr: ErrVersion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.build()
			require.ErrorIs(t, p.Err(), tt.wantErr)
		})
	}
}

func TestNew_EnumAcceptsValidValue(t *testing.T) {
	var env string
	p := New([]string{"--env", "prod"}, "app", "desc",
		AddFlag(&env, "env", "e", "dev", "", Enum("dev", "prod")))
	require.NoError(t, p.Err())
	assert.Equal(t, "prod", env)
}

func TestSubCommand_DispatchAndAlias(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "primary name", args: []string{"extract"}},
		{name: "alias", args: []string{"x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ran := false
			p := New(tt.args, "app", "desc",
				SubCommand("extract", "extract data",
					Alias("x"),
					Run(func(*Context) error { ran = true; return nil }),
				),
			)
			require.NoError(t, p.Err())
			require.NoError(t, p.Run())
			assert.True(t, ran)
			assert.Equal(t, "extract", p.Command().Name())
		})
	}
}

func TestFlagInheritance_ResolvesFromParent(t *testing.T) {
	var verbose bool
	p := New([]string{"serve", "--verbose"}, "app", "desc",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		SubCommand("serve", "start", Run(noopAction)),
	)
	require.NoError(t, p.Err())
	assert.True(t, verbose)
}

func TestIsSet_DistinguishesDefaultFromExplicit(t *testing.T) {
	t.Run("explicit zero value is set", func(t *testing.T) {
		var port int
		p := New([]string{"--port", "0"}, "app", "desc", AddFlag(&port, "port", "p", 8080, ""))
		require.NoError(t, p.Err())
		assert.Equal(t, 0, port)
		assert.True(t, p.IsSet("port"))
	})

	t.Run("default is not set", func(t *testing.T) {
		var port int
		p := New([]string{}, "app", "desc", AddFlag(&port, "port", "p", 8080, ""))
		require.NoError(t, p.Err())
		assert.False(t, p.IsSet("port"))
	})

	t.Run("unknown flag returns false", func(t *testing.T) {
		p := New([]string{}, "app", "desc")
		assert.False(t, p.IsSet("nope"))
	})
}

func TestReset_ReparsesAgainstSameTree(t *testing.T) {
	var port int
	var verbose bool
	p := New([]string{"--port", "1000", "--verbose"}, "app", "desc",
		AddFlag(&port, "port", "p", 8080, ""),
		AddFlag(&verbose, "verbose", "v", false, ""),
	)
	require.NoError(t, p.Err())
	require.Equal(t, 1000, port)
	require.True(t, verbose)

	require.NoError(t, p.Reset([]string{"--port", "2000"}))
	assert.Equal(t, 2000, port)
	assert.False(t, verbose, "verbose must be restored to default after reset")
	assert.True(t, p.IsSet("port"))
	assert.False(t, p.IsSet("verbose"))
}

func TestReset_ClearsPositionalArgs(t *testing.T) {
	p := New([]string{"a", "b"}, "app", "desc", Run(noopAction))
	require.NoError(t, p.Err())
	require.Equal(t, []string{"a", "b"}, p.Command().Args())

	require.NoError(t, p.Reset([]string{"c"}))
	assert.Equal(t, []string{"c"}, p.Command().Args())
}

func TestRun_NoActionReturnsNil(t *testing.T) {
	p := New([]string{}, "app", "desc")
	require.NoError(t, p.Err())
	assert.NoError(t, p.Run())
}

func TestRun_SkippedOnParseError(t *testing.T) {
	ran := false
	p := New([]string{"--bad"}, "app", "desc",
		SubCommand("x", "", Run(func(*Context) error { ran = true; return nil })),
	)
	require.Error(t, p.Err())
	assert.NoError(t, p.Run())
	assert.False(t, ran)
}

func TestCommandWithActionTreatsUnknownAsPositional(t *testing.T) {
	var collected []string
	p := New([]string{"file1", "file2"}, "app", "desc",
		Run(func(c *Context) error { collected = c.Args(); return nil }),
		SubCommand("sub", "", Run(noopAction)),
	)
	require.NoError(t, p.Err())
	require.NoError(t, p.Run())
	assert.Equal(t, []string{"file1", "file2"}, collected)
}

func TestContext_ZeroValueArgsNil(t *testing.T) {
	var c Context
	assert.Nil(t, c.Args())
}

func TestContext_AccessorsInsideAction(t *testing.T) {
	var (
		gotCmd    *Command
		gotParser *Parser
	)
	p := New([]string{"serve"}, "app", "desc",
		SubCommand("serve", "start server", Run(func(c *Context) error {
			gotCmd = c.Command()
			gotParser = c.Parser()
			return nil
		})),
	)
	require.NoError(t, p.Err())
	require.NoError(t, p.Run())
	require.NotNil(t, gotCmd)
	assert.Equal(t, "serve", gotCmd.Name())
	assert.Equal(t, "start server", gotCmd.Description())
	require.NotNil(t, gotCmd.Parent())
	assert.Equal(t, "app", gotCmd.Parent().Name())
	assert.Same(t, p, gotParser)
}

func TestFormatDefault_AllTypes(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want string
	}{
		{name: "bool empty", val: true, want: ""},
		{name: "empty string placeholder", val: "", want: "<string>"},
		{name: "non-empty string", val: "x", want: "[x]"},
		{name: "duration", val: 90 * time.Second, want: "[1m30s]"},
		{name: "time", val: time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC), want: "[2025-01-02T03:04:05Z]"},
		{name: "int fallback", val: 42, want: "[42]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatDefault(tt.val))
		})
	}
}

func TestHelp_RendersDefaultsRequiredAndEnum(t *testing.T) {
	var (
		host string
		env  string
		dur  time.Duration
	)
	p := New(nil, "app", "desc",
		AddFlag(&host, "host", "", "localhost", "server host"),
		AddFlag(&env, "env", "e", "dev", "target env", Required(), Enum("dev", "prod")),
		AddFlag(&dur, "dur", "d", 5*time.Second, "timeout"),
	)
	help := p.Help()
	assert.Contains(t, help, "[localhost]")
	assert.Contains(t, help, "(required)")
	assert.Contains(t, help, "one of:")
	assert.Contains(t, help, "[5s]")
}

func TestHelp_SubcommandWithAliasInList(t *testing.T) {
	p := New(nil, "app", "desc",
		SubCommand("extract", "extract data", Alias("x"), Run(noopAction)),
	)
	help := p.Help()
	assert.Contains(t, help, "COMMANDS:")
	assert.Contains(t, help, "extract, x")
}

func TestVersionAndHelpAccessors(t *testing.T) {
	var verbose bool
	p := New([]string{}, "myapp", "my tool",
		Version("2.3.4"),
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
	)
	require.NoError(t, p.Err())
	assert.Equal(t, "2.3.4", p.Version())
	assert.Contains(t, p.Help(), "USAGE: myapp")
	assert.Contains(t, p.Help(), "--verbose, -v")
}

func TestConstructionPanics(t *testing.T) {
	tests := []struct {
		name  string
		build func()
	}{
		{
			name: "unsupported type",
			build: func() {
				var x uint
				New(nil, "app", "desc", AddFlag(&x, "x", "", uint(0), ""))
			},
		},
		{
			name: "duplicate long flag",
			build: func() {
				var a, b int
				New(nil, "app", "desc",
					AddFlag(&a, "port", "", 0, ""),
					AddFlag(&b, "port", "", 0, ""),
				)
			},
		},
		{
			name: "duplicate short flag",
			build: func() {
				var a, b int
				New(nil, "app", "desc",
					AddFlag(&a, "port", "p", 0, ""),
					AddFlag(&b, "page", "p", 0, ""),
				)
			},
		},
		{
			name: "nil target",
			build: func() {
				New(nil, "app", "desc", AddFlag[int](nil, "port", "", 0, ""))
			},
		},
		{
			name: "duplicate subcommand",
			build: func() {
				//nolint:gocritic // intentional duplicate to trigger the panic
				New(nil, "app", "desc",
					SubCommand("serve", "", Run(noopAction)),
					SubCommand("serve", "", Run(noopAction)),
				)
			},
		},
		{
			name: "duplicate run",
			build: func() {
				//nolint:gocritic // intentional duplicate to trigger the panic
				New(nil, "app", "desc", Run(noopAction), Run(noopAction))
			},
		},
		{
			name: "enum type mismatch",
			build: func() {
				var env string
				New(nil, "app", "desc", AddFlag(&env, "env", "", "dev", "", Enum(1, 2)))
			},
		},
		{
			name: "duplicate alias",
			build: func() {
				New(nil, "app", "desc",
					SubCommand("a", "", Alias("x"), Run(noopAction)),
					SubCommand("b", "", Alias("x"), Run(noopAction)),
				)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Panics(t, tt.build)
		})
	}
}

func noopAction(*Context) error { return nil }
