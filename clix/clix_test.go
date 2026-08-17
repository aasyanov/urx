package clix

import (
	"strings"
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
		{name: "no- inline true", def: true, args: []string{"--no-verbose=true"}, want: false},
		{name: "no- inline false", def: true, args: []string{"--no-verbose=false"}, want: true},
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

	t.Run("h in group triggers help", func(t *testing.T) {
		var v bool
		p := New([]string{"-vh"}, "app", "desc", AddFlag(&v, "verbose", "v", false, ""))
		require.ErrorIs(t, p.Err(), ErrHelp)
		assert.True(t, v, "flags before h in the group must still be set")
	})

	t.Run("V in group triggers version", func(t *testing.T) {
		var v bool
		p := New([]string{"-vV"}, "app", "desc",
			Version("1.0"),
			AddFlag(&v, "verbose", "v", false, ""),
		)
		require.ErrorIs(t, p.Err(), ErrVersion)
		assert.True(t, v)
	})

	t.Run("trailing non-bool missing next arg", func(t *testing.T) {
		var v bool
		var port int
		p := New([]string{"-vp"}, "app", "desc",
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&port, "port", "p", 0, ""),
		)
		require.ErrorIs(t, p.Err(), ErrMissingValue)
	})

	t.Run("trailing non-bool next token is a flag", func(t *testing.T) {
		var port int
		var verbose bool
		p := New([]string{"-p", "-v"}, "app", "desc",
			AddFlag(&port, "port", "p", 0, ""),
			AddFlag(&verbose, "verbose", "v", false, ""),
		)
		require.ErrorIs(t, p.Err(), ErrMissingValue)
	})

	t.Run("non-bool first remainder is value", func(t *testing.T) {
		var v bool
		var port int
		p := New([]string{"-pv"}, "app", "desc",
			AddFlag(&v, "verbose", "v", false, ""),
			AddFlag(&port, "port", "p", 0, ""),
		)
		require.ErrorIs(t, p.Err(), ErrInvalidValue)
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

func TestNew_PositionalDashIsStdin(t *testing.T) {
	t.Run("root accepts dash", func(t *testing.T) {
		p := New([]string{"-"}, "app", "desc")
		require.NoError(t, p.Err())
		assert.Equal(t, []string{"-"}, p.Command().Args())
	})
	t.Run("routing node is unknown command", func(t *testing.T) {
		p := New([]string{"-"}, "app", "desc", SubCommand("serve", "s", Run(noopAction)))
		require.ErrorIs(t, p.Err(), ErrUnknownCommand)
	})
	t.Run("command with action and subs accepts dash", func(t *testing.T) {
		p := New([]string{"-"}, "app", "desc",
			Run(noopAction),
			SubCommand("serve", "s", Run(noopAction)),
		)
		require.NoError(t, p.Err())
		assert.Equal(t, []string{"-"}, p.Command().Args())
	})
}

func TestNew_DashAfterTerminator(t *testing.T) {
	p := New([]string{"--", "-"}, "app", "desc")
	require.NoError(t, p.Err())
	assert.Equal(t, []string{"-"}, p.Command().Args())
}

func TestNew_DashAsFlagValueStillBinds(t *testing.T) {
	var name string
	p := New([]string{"--name", "-"}, "app", "desc", AddFlag(&name, "name", "", "", ""))
	require.NoError(t, p.Err())
	assert.Equal(t, "-", name)
}

func TestNew_AllSupportedTypes(t *testing.T) {
	var (
		s  string
		i  int
		b  bool
		f  float64
		d  time.Duration
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
			name: "signed number token is unknown flag",
			build: func() *Parser {
				return New([]string{"-5"}, "app", "desc")
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
			name: "missing value followed by long flag",
			build: func() *Parser {
				var port int
				var verbose bool
				return New([]string{"--port", "--verbose"}, "app", "desc",
					AddFlag(&port, "port", "p", 0, ""),
					AddFlag(&verbose, "verbose", "v", false, ""),
				)
			},
			wantErr: ErrMissingValue,
		},
		{
			name: "missing value followed by short flag",
			build: func() *Parser {
				var name string
				var verbose bool
				return New([]string{"--name", "-v"}, "app", "desc",
					AddFlag(&name, "name", "", "", ""),
					AddFlag(&verbose, "verbose", "v", false, ""),
				)
			},
			wantErr: ErrMissingValue,
		},
		{
			name: "missing value followed by terminator",
			build: func() *Parser {
				var name string
				return New([]string{"--name", "--"}, "app", "desc", AddFlag(&name, "name", "", "", ""))
			},
			wantErr: ErrMissingValue,
		},
		{
			name: "helpers is not help",
			build: func() *Parser {
				return New([]string{"--helpers"}, "app", "desc")
			},
			wantErr: ErrUnknownFlag,
		},
		{
			name: "no- negation invalid bool",
			build: func() *Parser {
				var verbose bool
				return New([]string{"--no-verbose=notabool"}, "app", "desc",
					AddFlag(&verbose, "verbose", "v", false, ""),
				)
			},
			wantErr: ErrInvalidValue,
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
			name: "help inline equals",
			build: func() *Parser {
				return New([]string{"--help=true"}, "app", "desc")
			},
			wantErr: ErrHelp,
		},
		{
			name: "version inline equals",
			build: func() *Parser {
				return New([]string{"--version=1"}, "app", "desc", Version("1.0.0"))
			},
			wantErr: ErrVersion,
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
		{
			name: "version short",
			build: func() *Parser {
				return New([]string{"-V"}, "app", "desc", Version("1.0.0"))
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
	assert.True(t, p.IsSet("verbose"))
}

func TestFlagInheritance_FlagsBeforeSubcommand(t *testing.T) {
	var verbose bool
	p := New([]string{"--verbose", "serve"}, "app", "desc",
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		SubCommand("serve", "start", Run(noopAction)),
	)
	require.NoError(t, p.Err())
	assert.True(t, verbose)
	assert.Equal(t, "serve", p.Command().Name())
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

func TestIsSet_ShortAlias(t *testing.T) {
	var port int
	p := New([]string{"-p", "9"}, "app", "desc", AddFlag(&port, "port", "p", 0, ""))
	require.NoError(t, p.Err())
	assert.Equal(t, 9, port)
	assert.True(t, p.IsSet("p"))
	assert.True(t, p.IsSet("port"))
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
			assert.Equal(t, tt.want, formatDefault(tt.val, defaultLabelEmptyString))
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
	assert.Contains(t, help, "(one of: dev, prod)")
	assert.Contains(t, help, "[5s]")
	assert.Contains(t, help, "--help, -h")
}

func TestHelp_SubcommandWithAliasInList(t *testing.T) {
	p := New(nil, "app", "desc",
		SubCommand("extract", "extract data", Alias("x"), Run(noopAction)),
	)
	help := p.Help()
	assert.Contains(t, help, "COMMANDS:")
	assert.Contains(t, help, "extract, x")
}

func TestRun_PropagatesActionError(t *testing.T) {
	p := New([]string{}, "app", "desc", Run(func(*Context) error {
		return assert.AnError
	}))
	require.NoError(t, p.Err())
	require.ErrorIs(t, p.Run(), assert.AnError)
}

func TestSubCommand_NestedDispatch(t *testing.T) {
	ran := false
	p := New([]string{"db", "migrate"}, "app", "desc",
		SubCommand("db", "database",
			SubCommand("migrate", "run migrations", Run(func(*Context) error {
				ran = true
				return nil
			})),
		),
	)
	require.NoError(t, p.Err())
	require.NoError(t, p.Run())
	assert.True(t, ran)
	assert.Equal(t, "migrate", p.Command().Name())
	require.NotNil(t, p.Command().Parent())
	assert.Equal(t, "db", p.Command().Parent().Name())
}

func TestSubCommand_UnknownPositionalOnLeaf(t *testing.T) {
	p := New([]string{"db", "bogus"}, "app", "desc",
		SubCommand("db", "database",
			SubCommand("migrate", "run migrations", Run(noopAction)),
		),
	)
	require.ErrorIs(t, p.Err(), ErrUnknownCommand)
}

func TestRequired_InheritedFromParent(t *testing.T) {
	var host string
	p := New([]string{"serve"}, "app", "desc",
		AddFlag(&host, "host", "", "", "server host", Required()),
		SubCommand("serve", "start", Run(noopAction)),
	)
	require.ErrorIs(t, p.Err(), ErrRequired)
}

func TestAddFlag_LiteralDefOverwritesPointer(t *testing.T) {
	port := 9090
	p := New([]string{}, "app", "desc", AddFlag(&port, "port", "p", 8080, ""))
	require.NoError(t, p.Err())
	assert.Equal(t, 8080, port)
}

func TestAddFlag_CurrentValueAsDefPreserves(t *testing.T) {
	port := 9090
	p := New([]string{}, "app", "desc", AddFlag(&port, "port", "p", port, ""))
	require.NoError(t, p.Err())
	assert.Equal(t, 9090, port)
}

func TestRequired_IgnoresPrefilledPointer(t *testing.T) {
	port := 9090
	p := New([]string{}, "app", "desc", AddFlag(&port, "port", "p", port, "", Required()))
	require.ErrorIs(t, p.Err(), ErrRequired)
}

func TestNew_PartialParseLeavesEarlierFlags(t *testing.T) {
	var port int
	p := New([]string{"--port", "9090", "--nope"}, "app", "desc",
		AddFlag(&port, "port", "p", 8080, "listen port"),
	)
	require.ErrorIs(t, p.Err(), ErrUnknownFlag)
	assert.Equal(t, 9090, port, "flags parsed before the error must remain set")
}

func TestHelp_SubcommandGlobalFlags(t *testing.T) {
	var verbose bool
	p := New([]string{"serve", "--help"}, "app", "desc",
		AddFlag(&verbose, "verbose", "v", false, "verbose output"),
		SubCommand("serve", "start server", Run(noopAction)),
	)
	require.ErrorIs(t, p.Err(), ErrHelp)
	help := p.Help()
	assert.Contains(t, help, "USAGE: app serve")
	assert.Contains(t, help, "GLOBAL FLAGS:")
	assert.Contains(t, help, "--verbose, -v")
	assert.Contains(t, help, "--help, -h")
}

func TestHelp_MatchedSubcommandLevel(t *testing.T) {
	var port int
	p := New([]string{"serve", "--help"}, "app", "desc",
		SubCommand("serve", "start server",
			AddFlag(&port, "port", "p", 8080, "listen port"),
			Run(noopAction),
		),
	)
	require.ErrorIs(t, p.Err(), ErrHelp)
	assert.Contains(t, p.Help(), "USAGE: app serve")
	assert.Contains(t, p.Help(), "FLAGS:")
	assert.Contains(t, p.Help(), "--port, -p")
	assert.Contains(t, p.Help(), "--help, -h")
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
	assert.Contains(t, p.Help(), "--help, -h")
	assert.Contains(t, p.Help(), "--version, -V")
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
		{
			name: "empty command name",
			build: func() {
				New(nil, "", "desc")
			},
		},
		{
			name: "empty subcommand name",
			build: func() {
				New(nil, "app", "desc", SubCommand("", "desc", Run(noopAction)))
			},
		},
		{
			name: "empty flag name",
			build: func() {
				var port int
				New(nil, "app", "desc", AddFlag(&port, "", "p", 0, ""))
			},
		},
		{
			name: "empty alias",
			build: func() {
				New(nil, "app", "desc", SubCommand("serve", "", Alias(""), Run(noopAction)))
			},
		},
		{
			name: "shadow inherited long flag",
			build: func() {
				var rootPort, childPort int
				New(nil, "app", "desc",
					AddFlag(&rootPort, "port", "p", 8080, ""),
					SubCommand("serve", "start",
						AddFlag(&childPort, "port", "p", 9090, ""),
						Run(noopAction),
					),
				)
			},
		},
		{
			name: "shadow inherited short flag",
			build: func() {
				var verbose, childVerbose bool
				New(nil, "app", "desc",
					AddFlag(&verbose, "verbose", "v", false, ""),
					SubCommand("serve", "start",
						AddFlag(&childVerbose, "debug", "v", false, ""),
						Run(noopAction),
					),
				)
			},
		},
		{
			name: "reserved help long name",
			build: func() {
				var x bool
				New(nil, "app", "desc", AddFlag(&x, "help", "", false, ""))
			},
		},
		{
			name: "reserved help short",
			build: func() {
				var host string
				New(nil, "app", "desc", AddFlag(&host, "host", "h", "", ""))
			},
		},
		{
			name: "reserved version after Version option",
			build: func() {
				var x string
				New(nil, "app", "desc", Version("1"), AddFlag(&x, "version", "", "", ""))
			},
		},
		{
			name: "reserved V after Version option",
			build: func() {
				var x bool
				New(nil, "app", "desc", Version("1"), AddFlag(&x, "verbose", "V", false, ""))
			},
		},
		{
			name: "Version after user version flag",
			build: func() {
				var x string
				New(nil, "app", "desc", AddFlag(&x, "version", "", "", ""), Version("1"))
			},
		},
		{
			name: "Version on subcommand",
			build: func() {
				New(nil, "app", "desc",
					SubCommand("serve", "start", Version("1"), Run(noopAction)),
				)
			},
		},
		{
			name: "duplicate Version",
			build: func() {
				New(nil, "app", "desc", Version("1"), Version("2"))
			},
		},
		{
			name: "multi-character short",
			build: func() {
				var port int
				New(nil, "app", "desc", AddFlag(&port, "port", "po", 0, ""))
			},
		},
		{
			name: "WithHelpLabels on subcommand",
			build: func() {
				New(nil, "app", "desc",
					SubCommand("serve", "start", WithHelpLabels(HelpLabels{Usage: "U"}), Run(noopAction)),
				)
			},
		},
		{
			name: "Version after nested user version flag",
			build: func() {
				var tag string
				New(nil, "app", "desc",
					SubCommand("serve", "start",
						AddFlag(&tag, "version", "", "", ""),
						Run(noopAction),
					),
					Version("1"),
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

func TestNew_CustomVersionFlagWithoutBuiltin(t *testing.T) {
	var ver string
	p := New([]string{"--version", "nightly"}, "app", "desc",
		AddFlag(&ver, "version", "", "", "build tag"),
	)
	require.NoError(t, p.Err())
	assert.Equal(t, "nightly", ver)
	help := p.Help()
	assert.Contains(t, help, "--version")
	assert.Contains(t, help, "build tag")
	assert.NotContains(t, help, "-V")
	assert.NotContains(t, help, "print version")
}

func TestNew_NilOptionIgnored(t *testing.T) {
	p := New(nil, "app", "desc", nil, Version("1.0.0"))
	require.NoError(t, p.Err())
	assert.Equal(t, "1.0.0", p.Version())
	assert.Contains(t, p.Help(), "--version, -V")
}

func TestHelp_LegendBuiltinsAndPath(t *testing.T) {
	t.Run("bare always lists help not version", func(t *testing.T) {
		p := New(nil, "bare", "no flags no commands")
		help := p.Help()
		assert.Contains(t, help, "USAGE: bare [flags]\n")
		assert.Contains(t, help, "FLAGS:")
		assert.Contains(t, help, "--help, -h")
		assert.NotContains(t, help, "--version")
		assert.NotContains(t, help, "COMMANDS:")
		assert.NotContains(t, help, "GLOBAL FLAGS:")
	})

	t.Run("version appears only when Version is set", func(t *testing.T) {
		p := New(nil, "app", "desc", Version("1.2.3"))
		assert.Contains(t, p.Help(), "--help, -h")
		assert.Contains(t, p.Help(), "--version, -V")
		assert.Contains(t, p.Help(), "print version")
		assert.Contains(t, p.Help(), "show help")
	})

	t.Run("nested usage is full path and lists builtins plus globals", func(t *testing.T) {
		var verbose bool
		var steps int
		p := New([]string{"db", "migrate", "--help"}, "myapp", "my awesome tool",
			Version("1.2.3"),
			AddFlag(&verbose, "verbose", "v", false, "verbose output"),
			SubCommand("db", "database",
				AddFlag(&steps, "schema", "s", 0, "schema id"),
				SubCommand("migrate", "run migrations", Run(noopAction)),
			),
		)
		require.ErrorIs(t, p.Err(), ErrHelp)
		help := p.Help()
		assert.Contains(t, help, "USAGE: myapp db migrate [flags]\n")
		assert.Contains(t, help, "FLAGS:")
		assert.Contains(t, help, "--help, -h")
		assert.Contains(t, help, "--version, -V")
		assert.Contains(t, help, "GLOBAL FLAGS:")
		verboseIdx := strings.Index(help, "--verbose, -v")
		schemaIdx := strings.Index(help, "--schema, -s")
		require.Greater(t, verboseIdx, 0)
		require.Greater(t, schemaIdx, 0)
		assert.Less(t, verboseIdx, schemaIdx, "root globals must appear before parent flags")
	})

	t.Run("commands list aliases in registration order", func(t *testing.T) {
		p := New(nil, "app", "desc",
			SubCommand("extract", "extract data", Alias("x"), Run(noopAction)),
			SubCommand("db", "database", Run(noopAction)),
		)
		help := p.Help()
		assert.Contains(t, help, "USAGE: app [flags] [command]\n")
		assert.Contains(t, help, "COMMANDS:")
		extractIdx := strings.Index(help, "extract, x")
		dbIdx := strings.Index(help, "db")
		require.Greater(t, extractIdx, 0)
		assert.Less(t, extractIdx, dbIdx)
	})
}

func TestHelp_LabelsChrome(t *testing.T) {
	var host string
	p := New(nil, "app", "desc",
		WithHelpLabels(HelpLabels{
			Usage:          "ИСПОЛЬЗОВАНИЕ",
			Commands:       "КОМАНДЫ",
			Flags:          "ФЛАГИ",
			GlobalFlags:    "ГЛОБАЛЬНЫЕ ФЛАГИ",
			FlagsMetavar:   "[флаги]",
			CommandMetavar: "[команда]",
			HelpFlag:       "показать справку",
			VersionFlag:    "показать версию",
			Required:       "обязательный",
			OneOf:          "одно из: %s",
			EmptyString:    "<строка>",
		}),
		Version("1.0"),
		AddFlag(&host, "host", "", "", "server host", Required(), Enum("a", "b")),
		SubCommand("run", "start service", Alias("serve"), Run(noopAction)),
	)
	help := p.Help()
	assert.Contains(t, help, "ИСПОЛЬЗОВАНИЕ: app [флаги] [команда]\n")
	assert.Contains(t, help, "КОМАНДЫ:")
	assert.NotContains(t, help, "COMMANDS:")
	assert.Contains(t, help, "run, serve")
	assert.Contains(t, help, "ФЛАГИ:")
	assert.NotContains(t, help, "FLAGS:")
	assert.Contains(t, help, "показать справку")
	assert.Contains(t, help, "показать версию")
	assert.NotContains(t, help, "show help")
	assert.Contains(t, help, "(обязательный)")
	assert.Contains(t, help, "(одно из: a, b)")
	assert.Contains(t, help, "<строка>")
	assert.NotContains(t, help, "<string>")
}

func TestHelp_LabelsPartialFallsBackToEnglish(t *testing.T) {
	p := New(nil, "app", "desc",
		WithHelpLabels(HelpLabels{Usage: "USAGEX", HelpFlag: "aide"}),
		SubCommand("run", "start", Run(noopAction)),
	)
	help := p.Help()
	assert.Contains(t, help, "USAGEX: app [flags] [command]\n")
	assert.Contains(t, help, "COMMANDS:")
	assert.Contains(t, help, "FLAGS:")
	assert.Contains(t, help, "aide")
	assert.NotContains(t, help, "show help")
}

func TestHelp_LabelsApplyToNestedHelp(t *testing.T) {
	var verbose bool
	p := New([]string{"run", "--help"}, "app", "desc",
		WithHelpLabels(HelpLabels{Usage: "USAGEX", GlobalFlags: "GLOBALS", HelpFlag: "aide"}),
		AddFlag(&verbose, "verbose", "v", false, "verbose"),
		SubCommand("run", "start", Run(noopAction)),
	)
	require.ErrorIs(t, p.Err(), ErrHelp)
	help := p.Help()
	assert.Contains(t, help, "USAGEX: app run [flags]\n")
	assert.Contains(t, help, "GLOBALS:")
	assert.Contains(t, help, "--verbose, -v")
	assert.Contains(t, help, "aide")
}

func TestHelp_LabelsOneOfWithoutPercentS(t *testing.T) {
	var env string
	p := New(nil, "app", "desc",
		WithHelpLabels(HelpLabels{OneOf: "enum"}),
		AddFlag(&env, "env", "", "dev", "target", Enum("dev", "prod")),
	)
	assert.Contains(t, p.Help(), "(enum dev, prod)")
}

func TestHelp_LabelsSecondCallReplaces(t *testing.T) {
	p := New(nil, "app", "desc",
		WithHelpLabels(HelpLabels{Usage: "FIRST", HelpFlag: "one"}),
		WithHelpLabels(HelpLabels{Usage: "SECOND"}),
	)
	help := p.Help()
	assert.Contains(t, help, "SECOND: app")
	assert.NotContains(t, help, "FIRST:")
	assert.Contains(t, help, "show help")
	assert.NotContains(t, help, "one")
}

func TestNew_SignedNumericValues(t *testing.T) {
	t.Run("negative int", func(t *testing.T) {
		var offset int
		p := New([]string{"--offset", "-5"}, "app", "desc",
			AddFlag(&offset, "offset", "", 0, ""),
		)
		require.NoError(t, p.Err())
		assert.Equal(t, -5, offset)
	})
	t.Run("negative duration", func(t *testing.T) {
		var d time.Duration
		p := New([]string{"--wait", "-1s"}, "app", "desc",
			AddFlag(&d, "wait", "", time.Duration(0), ""),
		)
		require.NoError(t, p.Err())
		assert.Equal(t, -time.Second, d)
	})
	t.Run("bare dash is a string value", func(t *testing.T) {
		var name string
		p := New([]string{"--name", "-"}, "app", "desc",
			AddFlag(&name, "name", "", "", ""),
		)
		require.NoError(t, p.Err())
		assert.Equal(t, "-", name)
	})
	t.Run("inline equals binds a flag-like string", func(t *testing.T) {
		var name string
		p := New([]string{"--name=--raw"}, "app", "desc",
			AddFlag(&name, "name", "", "", ""),
		)
		require.NoError(t, p.Err())
		assert.Equal(t, "--raw", name)
	})
}

func TestNew_EmptyVersionIsNoop(t *testing.T) {
	p := New([]string{"--version"}, "app", "desc", Version(""))
	require.ErrorIs(t, p.Err(), ErrUnknownFlag)
	assert.Equal(t, "", p.Version())
}

func TestFormatOneOf(t *testing.T) {
	assert.Equal(t, "one of: a, b", formatOneOf("one of: %s", "a, b"))
	assert.Equal(t, "enum a, b", formatOneOf("enum", "a, b"))
	assert.Equal(t, "a, b", formatOneOf("", "a, b"))
}

func TestNextTokenLooksLikeFlag(t *testing.T) {
	assert.False(t, nextTokenLooksLikeFlag(""))
	assert.False(t, nextTokenLooksLikeFlag("-"))
	assert.False(t, nextTokenLooksLikeFlag("file"))
	assert.False(t, nextTokenLooksLikeFlag("-5"))
	assert.False(t, nextTokenLooksLikeFlag("-1s"))
	assert.True(t, nextTokenLooksLikeFlag("--"))
	assert.True(t, nextTokenLooksLikeFlag("--verbose"))
	assert.True(t, nextTokenLooksLikeFlag("-v"))
}

func noopAction(*Context) error { return nil }
