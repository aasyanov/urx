// Package cfgx loads and saves configuration files in YAML, JSON, or TOML
// format for industrial Go services.
//
// cfgx handles one step in the configuration pipeline: reading a file into
// a struct and writing it back. It composes with [envx] (environment
// overrides) and [clix] (CLI flag overrides) through simple pointer sharing
// — the user orchestrates the pipeline in main().
//
// # Quick start
//
//	cfg := NewDefaultConfig()
//	if err := cfgx.Load("config.yaml", &cfg); err != nil {
//	    log.Fatal(err)
//	}
//
// # Validate and autofix
//
// If the destination struct implements [Validator], Load calls it
// automatically after unmarshalling:
//
//	type Config struct {
//	    Port int `yaml:"port"`
//	}
//
//	func (c *Config) Validate(fix bool) []error {
//	    if c.Port <= 0 {
//	        if fix { c.Port = 8080 }
//	        return []error{fmt.Errorf("port must be > 0")}
//	    }
//	    return nil
//	}
//
// # Save after fix
//
//	cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix())
//	cfgx.Save("config.yaml", &cfg)
//
// # Format detection
//
// The format is detected from the file extension (.yaml/.yml, .json, .toml).
// Use [WithFormat] to override.
//
// # Testing
//
// Inject file I/O with [WithReader] and [WithWriter] to avoid touching
// the real filesystem:
//
//	data := []byte(`{"port": 9090}`)
//	cfgx.Load("config.json", &cfg,
//	    cfgx.WithReader(func(string) ([]byte, error) { return data, nil }),
//	)
package cfgx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// --- Format ---

// Format identifies a configuration file encoding.
type Format uint8

const (
	// FormatAuto detects format from the file extension.
	FormatAuto Format = iota
	// FormatYAML selects YAML encoding.
	FormatYAML
	// FormatJSON selects JSON encoding.
	FormatJSON
	// FormatTOML selects TOML encoding.
	FormatTOML
)

// --- Validator ---

// Validator is an optional interface that config structs can implement.
// [Load] calls it automatically after unmarshalling when [WithAutoFix]
// is enabled (fix=true) or by default without fix (fix=false).
//
// Implementations should use [validx] functions internally for field
// checks. When fix is true, the method may mutate the receiver to
// correct invalid values and should return only errors that remain
// after the fix pass.
type Validator interface {
	Validate(fix bool) []error
}

// --- Options ---

type config struct {
	format    Format
	autoFix   bool
	createOK  bool
	reader    func(string) ([]byte, error)
	writer    func(string, []byte, os.FileMode) error
}

func defaultConfig() config {
	return config{
		reader: os.ReadFile,
		writer: os.WriteFile,
	}
}

// Option configures [Load] or [Save] behavior.
type Option func(*config)

// WithFormat forces a specific format instead of auto-detecting from
// the file extension.
func WithFormat(f Format) Option {
	return func(c *config) { c.format = f }
}

// WithAutoFix enables automatic fixing when the destination struct
// implements [Validator]. Without this option, Validate is called
// with fix=false (report only).
// Any validation errors returned by Validate are propagated by [Load].
func WithAutoFix() Option {
	return func(c *config) { c.autoFix = true }
}

// WithCreateIfMissing makes [Load] write the destination struct
// (with its current default values) to disk when the file does not
// exist, instead of returning an error.
func WithCreateIfMissing() Option {
	return func(c *config) { c.createOK = true }
}

// WithReader replaces the default file reader (os.ReadFile).
// Useful for testing without touching the filesystem.
func WithReader(fn func(path string) ([]byte, error)) Option {
	return func(c *config) {
		if fn != nil {
			c.reader = fn
		}
	}
}

// WithWriter replaces the default file writer (os.WriteFile).
// Useful for testing without touching the filesystem.
func WithWriter(fn func(path string, data []byte, perm os.FileMode) error) Option {
	return func(c *config) {
		if fn != nil {
			c.writer = fn
		}
	}
}

// --- Load ---

// Load reads a config file at path into dst. The format is detected
// from the file extension unless overridden with [WithFormat].
//
// If the file does not exist and [WithCreateIfMissing] is set, the
// destination is validated (if it implements [Validator]) and then
// written to disk in the detected format — saving corrected defaults.
//
// If the destination implements [Validator], it is called after
// successful unmarshal (or before writing when creating a missing file).
// Any returned validation errors are propagated as an [*errx.MultiError].
//
// Errors are returned as [*errx.Error] values (domain [DomainConfig]),
// or [*errx.MultiError] when validation reports multiple issues.
func Load(path string, dst any, opts ...Option) error {
	if dst == nil {
		return errInvalidInput("dst", "must be a non-nil pointer")
	}
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return errInvalidInput("dst", "must be a non-nil pointer")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	format, err := resolveFormat(cfg.format, path)
	if err != nil {
		return err
	}

	data, readErr := cfg.reader(path)
	if readErr != nil {
		if os.IsNotExist(readErr) && cfg.createOK {
			if v, ok := dst.(Validator); ok {
				if err := errValidationFailed(path, v.Validate(cfg.autoFix)); err != nil {
					return err
				}
			}
			return save(path, dst, format, cfg.writer)
		}
		if os.IsNotExist(readErr) {
			return errNotFound(path)
		}
		return errReadFailed(path, readErr)
	}

	if unmarshalErr := unmarshal(data, dst, format); unmarshalErr != nil {
		return errParseFailed(path, unmarshalErr)
	}

	if v, ok := dst.(Validator); ok {
		if err := errValidationFailed(path, v.Validate(cfg.autoFix)); err != nil {
			return err
		}
	}

	return nil
}

// --- Save ---

// Save writes src to a file at path in the format detected from the
// extension (or forced with [WithFormat]). Useful after Validate(fix=true)
// to persist auto-corrected values.
func Save(path string, src any, opts ...Option) error {
	if src == nil {
		return errInvalidInput("src", "must be non-nil")
	}
	rv := reflect.ValueOf(src)
	if rv.Kind() == reflect.Ptr && rv.IsNil() {
		return errInvalidInput("src", "must be non-nil")
	}

	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	format, err := resolveFormat(cfg.format, path)
	if err != nil {
		return err
	}

	return save(path, src, format, cfg.writer)
}

// --- Internal ---

func save(path string, src any, format Format, writer func(string, []byte, os.FileMode) error) error {
	data, err := marshal(src, format)
	if err != nil {
		return errWriteFailed(path, err)
	}
	if writeErr := writer(path, data, 0644); writeErr != nil {
		return errWriteFailed(path, writeErr)
	}
	return nil
}

func resolveFormat(f Format, path string) (Format, error) {
	if f != FormatAuto {
		return f, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return FormatYAML, nil
	case ".json":
		return FormatJSON, nil
	case ".toml":
		return FormatTOML, nil
	default:
		return FormatAuto, errUnsupportedFormat(path, ext)
	}
}

func unmarshal(data []byte, dst any, format Format) error {
	switch format {
	case FormatYAML:
		return yaml.Unmarshal(data, dst)
	case FormatJSON:
		return json.Unmarshal(data, dst)
	case FormatTOML:
		return toml.Unmarshal(data, dst)
	default:
		panic(fmt.Sprintf("cfgx: unmarshal called with unresolved format %d", format))
	}
}

func marshal(src any, format Format) ([]byte, error) {
	switch format {
	case FormatYAML:
		return yaml.Marshal(src)
	case FormatJSON:
		data, err := json.MarshalIndent(src, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(data, '\n'), nil
	case FormatTOML:
		var buf bytes.Buffer
		enc := toml.NewEncoder(&buf)
		if err := enc.Encode(src); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		panic(fmt.Sprintf("cfgx: marshal called with unresolved format %d", format))
	}
}
