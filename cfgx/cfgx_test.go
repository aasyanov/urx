package cfgx

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testConfig struct {
	Port int    `yaml:"port" json:"port" toml:"port"`
	Host string `yaml:"host" json:"host" toml:"host"`
}

// fixConfig implements Validator: it clamps Port and reports the violation.
type fixConfig struct {
	Port int `yaml:"port" json:"port" toml:"port"`
}

func (c *fixConfig) Validate(fix bool) []error {
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
		}
		return []error{errors.New("port must be > 0")}
	}
	return nil
}

// autofixCleanConfig repairs Port when fix=true and reports success (nil).
type autofixCleanConfig struct {
	Port int `yaml:"port" json:"port" toml:"port"`
}

func (c *autofixCleanConfig) Validate(fix bool) []error {
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
			return nil
		}
		return []error{errors.New("port must be > 0")}
	}
	return nil
}

func staticReader(data []byte, err error) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return data, err }
}

func TestLoad_DecodesAllFormats(t *testing.T) {
	tests := []struct {
		name string
		path string
		data string
	}{
		{name: "yaml", path: "config.yaml", data: "port: 9090\nhost: db.local\n"},
		{name: "yml", path: "config.yml", data: "port: 9090\nhost: db.local\n"},
		{name: "json", path: "config.json", data: `{"port":9090,"host":"db.local"}`},
		{name: "toml", path: "config.toml", data: "port = 9090\nhost = \"db.local\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg testConfig
			err := Load(tt.path, &cfg, WithReader(staticReader([]byte(tt.data), nil)))
			require.NoError(t, err)
			assert.Equal(t, 9090, cfg.Port)
			assert.Equal(t, "db.local", cfg.Host)
		})
	}
}

func TestLoad_FormatOverride(t *testing.T) {
	var cfg testConfig
	// Extension is .conf (unknown) but we force JSON.
	err := Load("config.conf", &cfg,
		WithFormat(FormatJSON),
		WithReader(staticReader([]byte(`{"port":1}`), nil)),
	)
	require.NoError(t, err)
	assert.Equal(t, 1, cfg.Port)
}

func TestLoad_PreservesDefaultsForAbsentFields(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Load("config.yaml", &cfg, WithReader(staticReader([]byte("port: 3000\n"), nil)))
	require.NoError(t, err)
	assert.Equal(t, 3000, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host, "absent field keeps default")
}

func TestLoad_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{
			name:    "nil dst",
			run:     func() error { return Load("c.yaml", nil) },
			wantErr: ErrInvalidInput,
		},
		{
			name: "non-pointer dst",
			run: func() error {
				return Load("c.yaml", testConfig{}, WithReader(staticReader([]byte("port: 1"), nil)))
			},
			wantErr: ErrInvalidInput,
		},
		{
			name: "nil pointer dst",
			run: func() error {
				var p *testConfig
				return Load("c.yaml", p)
			},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "unsupported extension",
			run:     func() error { var c testConfig; return Load("c.conf", &c) },
			wantErr: ErrUnsupportedFormat,
		},
		{
			name: "not found",
			run: func() error {
				var c testConfig
				return Load("missing.yaml", &c, WithReader(staticReader(nil, os.ErrNotExist)))
			},
			wantErr: ErrNotFound,
		},
		{
			name: "read failed",
			run: func() error {
				var c testConfig
				return Load("c.yaml", &c, WithReader(staticReader(nil, errors.New("disk error"))))
			},
			wantErr: ErrReadFailed,
		},
		{
			name: "parse failed",
			run: func() error {
				var c testConfig
				return Load("c.json", &c, WithReader(staticReader([]byte("{not json"), nil)))
			},
			wantErr: ErrParseFailed,
		},
		{
			name: "validation failed",
			run: func() error {
				c := &fixConfig{Port: -1}
				return Load("c.yaml", c, WithReader(staticReader([]byte("port: -1\n"), nil)))
			},
			wantErr: ErrValidationFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.run(), tt.wantErr)
		})
	}
}

func TestLoad_AutoFixRepairsAndReportsRemaining(t *testing.T) {
	c := &fixConfig{}
	err := Load("c.yaml", c,
		WithAutoFix(),
		WithReader(staticReader([]byte("port: 0\n"), nil)),
	)
	// Validate still returns the original error even after fixing.
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 8080, c.Port, "autofix must repair the field")
}

func TestLoad_ValidateReportOnlyDoesNotMutate(t *testing.T) {
	c := &fixConfig{}
	err := Load("c.yaml", c, WithReader(staticReader([]byte("port: 0\n"), nil)))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 0, c.Port, "without WithAutoFix the struct is not mutated")
}

func TestLoad_CreateIfMissingWritesDefaults(t *testing.T) {
	var written struct {
		path string
		data []byte
		mode os.FileMode
	}
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Load("new.yaml", &cfg,
		WithCreateIfMissing(),
		WithReader(staticReader(nil, os.ErrNotExist)),
		WithWriter(func(p string, d []byte, m os.FileMode) error {
			written.path, written.data, written.mode = p, d, m
			return nil
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, "new.yaml", written.path)
	assert.Contains(t, string(written.data), "port: 8080")
	assert.Equal(t, defaultFileMode, written.mode)
}

func TestLoad_CreateIfMissingValidatesBeforeWrite(t *testing.T) {
	c := &fixConfig{Port: -5}
	wrote := false
	err := Load("new.yaml", c,
		WithCreateIfMissing(),
		WithReader(staticReader(nil, os.ErrNotExist)),
		WithWriter(func(string, []byte, os.FileMode) error { wrote = true; return nil }),
	)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.False(t, wrote, "must not write when validation fails without autofix")
}

func TestLoad_CreateIfMissingWriteError(t *testing.T) {
	cfg := testConfig{Port: 1}
	err := Load("new.yaml", &cfg,
		WithCreateIfMissing(),
		WithReader(staticReader(nil, os.ErrNotExist)),
		WithWriter(func(string, []byte, os.FileMode) error { return errors.New("readonly fs") }),
	)
	require.ErrorIs(t, err, ErrWriteFailed)
}

func TestParse_DecodesBytes(t *testing.T) {
	var cfg testConfig
	err := Parse([]byte(`{"port":7,"host":"h"}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err)
	assert.Equal(t, 7, cfg.Port)
	assert.Equal(t, "h", cfg.Host)
}

func TestParse_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{
			name:    "nil dst",
			run:     func() error { return Parse([]byte("{}"), nil, WithFormat(FormatJSON)) },
			wantErr: ErrInvalidInput,
		},
		{
			name:    "auto format rejected",
			run:     func() error { var c testConfig; return Parse([]byte("{}"), &c) },
			wantErr: ErrUnsupportedFormat,
		},
		{
			name:    "bad payload",
			run:     func() error { var c testConfig; return Parse([]byte("{bad"), &c, WithFormat(FormatJSON)) },
			wantErr: ErrParseFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.run(), tt.wantErr)
		})
	}
}

func TestParse_RunsValidator(t *testing.T) {
	c := &fixConfig{}
	err := Parse([]byte(`{"port":0}`), c, WithFormat(FormatJSON), WithAutoFix())
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 8080, c.Port)
}

func TestSave_WritesResolvedFormat(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		format Format
		want   string
	}{
		{name: "yaml", path: "out.yaml", want: "port: 5"},
		{name: "json", path: "out.json", want: `"port": 5`},
		{name: "toml", path: "out.toml", want: "port = 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []byte
			cfg := testConfig{Port: 5, Host: "h"}
			err := Save(tt.path, &cfg, WithWriter(func(_ string, d []byte, _ os.FileMode) error {
				got = d
				return nil
			}))
			require.NoError(t, err)
			assert.Contains(t, string(got), tt.want)
		})
	}
}

func TestSave_CustomFileMode(t *testing.T) {
	var gotMode os.FileMode
	cfg := testConfig{Port: 1}
	err := Save("out.yaml", &cfg,
		WithFileMode(0o600),
		WithWriter(func(_ string, _ []byte, m os.FileMode) error { gotMode = m; return nil }),
	)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), gotMode)
}

func TestSave_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{
			name:    "nil src",
			run:     func() error { return Save("o.yaml", nil) },
			wantErr: ErrInvalidInput,
		},
		{
			name: "nil pointer src",
			run: func() error {
				var p *testConfig
				return Save("o.yaml", p)
			},
			wantErr: ErrInvalidInput,
		},
		{
			name:    "unsupported extension",
			run:     func() error { return Save("o.conf", &testConfig{}) },
			wantErr: ErrUnsupportedFormat,
		},
		{
			name: "write error",
			run: func() error {
				return Save("o.yaml", &testConfig{}, WithWriter(func(string, []byte, os.FileMode) error {
					return errors.New("nope")
				}))
			},
			wantErr: ErrWriteFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.run(), tt.wantErr)
		})
	}
}

func TestMarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		format Format
	}{
		{name: "yaml", format: FormatYAML},
		{name: "json", format: FormatJSON},
		{name: "toml", format: FormatTOML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := testConfig{Port: 4242, Host: "round.trip"}
			data, err := Marshal(&in, tt.format)
			require.NoError(t, err)

			var out testConfig
			require.NoError(t, Parse(data, &out, WithFormat(tt.format)))
			assert.Equal(t, in, out)
		})
	}
}

func TestMarshal_ErrorPaths(t *testing.T) {
	t.Run("nil src", func(t *testing.T) {
		_, err := Marshal(nil, FormatJSON)
		require.ErrorIs(t, err, ErrInvalidInput)
	})
	t.Run("auto format", func(t *testing.T) {
		_, err := Marshal(&testConfig{}, FormatAuto)
		require.ErrorIs(t, err, ErrUnsupportedFormat)
	})
	t.Run("unencodable value", func(t *testing.T) {
		_, err := Marshal(map[string]any{"fn": func() {}}, FormatJSON)
		require.ErrorIs(t, err, ErrWriteFailed)
	})
	t.Run("unencodable value yaml", func(t *testing.T) {
		_, err := Marshal(map[string]any{"fn": func() {}}, FormatYAML)
		require.ErrorIs(t, err, ErrWriteFailed)
	})
	t.Run("unencodable value toml", func(t *testing.T) {
		_, err := Marshal(map[string]any{"fn": func() {}}, FormatTOML)
		require.ErrorIs(t, err, ErrWriteFailed)
	})
}

func TestFormat_String(t *testing.T) {
	tests := []struct {
		f    Format
		want string
	}{
		{FormatAuto, "auto"},
		{FormatYAML, "yaml"},
		{FormatJSON, "json"},
		{FormatTOML, "toml"},
		{Format(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.f.String())
		})
	}
}

func TestWithReaderWriter_NilIgnored(t *testing.T) {
	cfg := defaultConfig()
	WithReader(nil)(&cfg)
	WithWriter(nil)(&cfg)
	assert.NotNil(t, cfg.reader)
	assert.NotNil(t, cfg.writer)
}

func TestLoad_NoValidatorIsFine(t *testing.T) {
	var cfg testConfig // does not implement Validator
	err := Load("c.yaml", &cfg, WithReader(staticReader([]byte("port: 1\n"), nil)))
	require.NoError(t, err)
}

func TestParse_EmptyPayloadPerCodec(t *testing.T) {
	tests := []struct {
		name    string
		format  Format
		wantErr error
	}{
		{name: "yaml accepts empty", format: FormatYAML, wantErr: nil},
		{name: "toml accepts empty", format: FormatTOML, wantErr: nil},
		{name: "json rejects empty", format: FormatJSON, wantErr: ErrParseFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig{Port: 99, Host: "keep"}
			err := Parse([]byte(""), &cfg, WithFormat(tt.format))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, 99, cfg.Port, "decode failure must not mutate dst")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, 99, cfg.Port)
		})
	}
}

func TestLoad_ExtensionlessPath(t *testing.T) {
	var cfg testConfig
	err := Load("config", &cfg, WithReader(staticReader([]byte("port: 1\n"), nil)))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestLoad_CreateIfMissingAutoFixWritesRepaired(t *testing.T) {
	var written []byte
	c := &autofixCleanConfig{}
	err := Load("new.yaml", c,
		WithCreateIfMissing(),
		WithAutoFix(),
		WithReader(staticReader(nil, os.ErrNotExist)),
		WithWriter(func(_ string, d []byte, _ os.FileMode) error {
			written = d
			return nil
		}),
	)
	require.NoError(t, err)
	assert.Equal(t, 8080, c.Port)
	assert.Contains(t, string(written), "port: 8080")
}

func TestSave_MarshalFailure(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		format Format
		src    any
	}{
		{name: "json", path: "out.json", format: FormatJSON, src: map[string]any{"fn": func() {}}},
		{name: "yaml", path: "out.yaml", format: FormatYAML, src: map[string]any{"fn": func() {}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Save(tt.path, tt.src,
				WithFormat(tt.format),
				WithWriter(func(string, []byte, os.FileMode) error {
					t.Fatal("writer must not run when marshal fails")
					return nil
				}),
			)
			require.ErrorIs(t, err, ErrWriteFailed)
		})
	}
}

func TestSave_FormatOverrideUnknownExtension(t *testing.T) {
	var got []byte
	cfg := testConfig{Port: 7, Host: "h"}
	err := Save("out.conf", &cfg,
		WithFormat(FormatYAML),
		WithWriter(func(_ string, d []byte, _ os.FileMode) error {
			got = d
			return nil
		}),
	)
	require.NoError(t, err)
	assert.Contains(t, string(got), "port: 7")
}

func TestSave_AcceptsValueType(t *testing.T) {
	var got []byte
	cfg := testConfig{Port: 3}
	err := Save("out.yaml", cfg, WithWriter(func(_ string, d []byte, _ os.FileMode) error {
		got = d
		return nil
	}))
	require.NoError(t, err)
	assert.Contains(t, string(got), "port: 3")
}

func TestMarshal_TrailingNewline(t *testing.T) {
	cfg := testConfig{Port: 1, Host: "h"}
	for _, format := range []Format{FormatYAML, FormatJSON, FormatTOML} {
		t.Run(format.String(), func(t *testing.T) {
			data, err := Marshal(&cfg, format)
			require.NoError(t, err)
			require.NotEmpty(t, data)
			assert.Equal(t, byte('\n'), data[len(data)-1])
		})
	}
}

func TestOptions_Defaults(t *testing.T) {
	cfg := defaultConfig()
	assert.Equal(t, FormatAuto, cfg.format)
	assert.False(t, cfg.autoFix)
	assert.False(t, cfg.createOK)
	assert.Equal(t, defaultFileMode, cfg.fileMode)
	assert.NotNil(t, cfg.reader)
	assert.NotNil(t, cfg.writer)
}

func TestWithFormat_AppliesToAllOperations(t *testing.T) {
	t.Run("parse", func(t *testing.T) {
		var cfg testConfig
		err := Parse([]byte(`{"port":2}`), &cfg, WithFormat(FormatJSON))
		require.NoError(t, err)
		assert.Equal(t, 2, cfg.Port)
	})
	t.Run("save", func(t *testing.T) {
		var got []byte
		cfg := testConfig{Port: 4}
		err := Save("x.conf", &cfg,
			WithFormat(FormatJSON),
			WithWriter(func(_ string, d []byte, _ os.FileMode) error { got = d; return nil }),
		)
		require.NoError(t, err)
		assert.Contains(t, string(got), `"port": 4`)
	})
}

func TestUnmarshal_UnresolvedFormatPanics(t *testing.T) {
	require.Panics(t, func() {
		_ = unmarshal([]byte("port: 1"), &testConfig{}, FormatAuto)
	})
}

func TestMarshal_UnresolvedFormatPanics(t *testing.T) {
	require.Panics(t, func() {
		_, _ = marshal(&testConfig{}, FormatAuto)
	})
}

func TestLoad_NilOptionsIgnored(t *testing.T) {
	var cfg testConfig
	var none Option
	err := Load("c.json", &cfg,
		none,
		WithFormat(FormatJSON),
		WithReader(staticReader([]byte(`{"port":3}`), nil)),
	)
	require.NoError(t, err)
	assert.Equal(t, 3, cfg.Port)
}

func TestLoad_InvalidFormat(t *testing.T) {
	var cfg testConfig
	err := Load("c.yaml", &cfg, WithFormat(Format(99)), WithReader(staticReader([]byte("port: 1\n"), nil)))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestParse_InvalidFormat(t *testing.T) {
	var cfg testConfig
	err := Parse([]byte(`{"port":1}`), &cfg, WithFormat(Format(99)))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestSave_InvalidFormat(t *testing.T) {
	err := Save("o.conf", &testConfig{}, WithFormat(Format(99)), WithWriter(func(string, []byte, os.FileMode) error {
		t.Fatal("writer must not run")
		return nil
	}))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestMarshal_InvalidFormat(t *testing.T) {
	_, err := Marshal(&testConfig{}, Format(99))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestSave_CreatesParentDirectories(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "out.yaml")
	cfg := testConfig{Port: 8, Host: "h"}
	require.NoError(t, Save(path, &cfg))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(data), "port: 8")
}

func TestSave_BareFilenameNoMkdirAll(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := testConfig{Port: 2}
	require.NoError(t, Save("out.yaml", &cfg))
	data, err := os.ReadFile("out.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "port: 2")
}

func TestLoad_CreateIfMissingAutoFixRemainingDoesNotWrite(t *testing.T) {
	c := &fixConfig{Port: -5}
	wrote := false
	err := Load("new.yaml", c,
		WithCreateIfMissing(),
		WithAutoFix(),
		WithReader(staticReader(nil, os.ErrNotExist)),
		WithWriter(func(string, []byte, os.FileMode) error { wrote = true; return nil }),
	)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 8080, c.Port, "autofix still repairs in memory")
	assert.False(t, wrote, "must not persist when validation errors remain")
}

func TestLoad_PreservesDefaultsJSON(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Load("config.json", &cfg, WithReader(staticReader([]byte(`{"port":3000}`), nil)))
	require.NoError(t, err)
	assert.Equal(t, 3000, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host, "absent field keeps default")
}

func TestLoad_PreservesDefaultsTOML(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Load("config.toml", &cfg, WithReader(staticReader([]byte("port = 3000\n"), nil)))
	require.NoError(t, err)
	assert.Equal(t, 3000, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host, "absent field keeps default")
}

func TestParse_JSONNullDoesNotZeroScalar(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Parse([]byte(`{"port":null}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host)
}

func TestParse_YAMLNullDoesNotZeroScalar(t *testing.T) {
	tests := []struct {
		name string
		data string
	}{
		{name: "null", data: "port: null\n"},
		{name: "tilde", data: "port: ~\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig{Port: 8080, Host: "localhost"}
			err := Parse([]byte(tt.data), &cfg, WithFormat(FormatYAML))
			require.NoError(t, err)
			assert.Equal(t, 8080, cfg.Port)
			assert.Equal(t, "localhost", cfg.Host)
		})
	}
}

func TestParse_YAMLEmptyKeyDoesNotZeroScalar(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Parse([]byte("port:\n"), &cfg, WithFormat(FormatYAML))
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host)
}

func TestParse_YAMLQuotedEmptyZerosString(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Parse([]byte("host: \"\"\n"), &cfg, WithFormat(FormatYAML))
	require.NoError(t, err)
	assert.Equal(t, 8080, cfg.Port)
	assert.Equal(t, "", cfg.Host)
}

func TestParse_JSONNullPointerBecomesNil(t *testing.T) {
	keep := "keep"
	cfg := struct {
		Host *string `json:"host"`
	}{Host: &keep}
	err := Parse([]byte(`{"host":null}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err)
	assert.Nil(t, cfg.Host)
}

func TestParse_YAMLNullPointerBecomesNil(t *testing.T) {
	keep := "keep"
	cfg := struct {
		Host *string `yaml:"host"`
	}{Host: &keep}
	err := Parse([]byte("host: null\n"), &cfg, WithFormat(FormatYAML))
	require.NoError(t, err)
	assert.Nil(t, cfg.Host)
}

func TestParse_UnknownJSONKeyIgnored(t *testing.T) {
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Parse([]byte(`{"port":9,"unknown":true}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err)
	assert.Equal(t, 9, cfg.Port)
	assert.Equal(t, "localhost", cfg.Host)
}

func TestMarshal_NilPointerRejected(t *testing.T) {
	var p *testConfig
	_, err := Marshal(p, FormatJSON)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestLoad_WrappedNotExistCreatesIfMissing(t *testing.T) {
	var written []byte
	cfg := testConfig{Port: 8080, Host: "localhost"}
	err := Load("new.yaml", &cfg,
		WithCreateIfMissing(),
		WithReader(staticReader(nil, fmt.Errorf("missing: %w", os.ErrNotExist))),
		WithWriter(func(_ string, d []byte, _ os.FileMode) error {
			written = d
			return nil
		}),
	)
	require.NoError(t, err)
	require.NotEmpty(t, written)
	assert.Contains(t, string(written), "port: 8080")
}

func TestLoad_WrappedNotExistWithoutCreate(t *testing.T) {
	var cfg testConfig
	err := Load("missing.yaml", &cfg, WithReader(staticReader(nil, fmt.Errorf("missing: %w", os.ErrNotExist))))
	require.ErrorIs(t, err, ErrNotFound)
	require.NotErrorIs(t, err, ErrReadFailed)
}
