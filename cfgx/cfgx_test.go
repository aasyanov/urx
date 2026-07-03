package cfgx

import (
	"errors"
	"os"
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
