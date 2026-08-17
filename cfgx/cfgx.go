// Package cfgx loads and saves configuration files in YAML, JSON, or TOML
// format for production Go services.
//
// cfgx owns exactly one step in the configuration pipeline: decoding a file
// (or byte slice) into a struct and encoding it back. It deliberately does
// not read environment variables or parse CLI flags — those are the jobs of
// envx and clix. The three compose through plain pointer sharing, so cfgx
// imports neither of them.
//
// # Quick start
//
//	cfg := Config{Port: 8080}
//	if err := cfgx.Load("config.yaml", &cfg); err != nil {
//	    log.Fatal(err)
//	}
//
// # The precedence pipeline (cfgx → envx → clix)
//
// The canonical 12-factor order is: built-in defaults, then file, then
// environment, then command-line flags — each layer overriding the previous.
// Because every layer writes through pointers into the same struct, a
// composition root looks like this:
//
//	cfg := DefaultConfig() // compiled-in defaults
//
//	if err := cfgx.Load("config.yaml", &cfg, cfgx.WithCreateIfMissing()); err != nil {
//	    if !errors.Is(err, cfgx.ErrValidationFailed) {
//	        log.Fatal(err) // I/O / parse / format — do not continue on partial decode
//	    }
//	    // optional: still continue so env/flags can repair file-only validation
//	}
//
//	env := envx.New(envx.WithPrefix("APP"))
//	envx.BindTo(env, "PORT", &cfg.Port) // absent keeps file
//	envx.BindTo(env, "HOST", &cfg.Host)
//
//	p := clix.New(os.Args[1:], "app", "my service",
//	    clix.AddFlag(&cfg.Port, "port", "p", cfg.Port, "listen port"), // def MUST be live field
//	    clix.AddFlag(&cfg.Host, "host", "", cfg.Host, "bind host"),
//	)
//
//	if errors.Is(p.Err(), clix.ErrHelp) {
//	    fmt.Print(p.Help())
//	    return
//	}
//	if errors.Is(p.Err(), clix.ErrVersion) {
//	    fmt.Println(p.Version())
//	    return
//	}
//	if err := errors.Join(env.Validate(), p.Err()); err != nil {
//	    log.Fatal(err)
//	}
//	if err := cfgx.Validate(&cfg, false); err != nil {
//	    log.Fatal(err)
//	}
//
// clix.Required is CLI-presence only. Share only string, int, bool, float64,
// time.Duration, and time.Time with clix.AddFlag. YAML duration 30s works;
// JSON needs nanoseconds or a string you parse. Do not [Validate] with
// fix=true after flags.
//
// Each layer is independent and testable in isolation; cfgx provides only the
// file/byte step and the [Validator] seam they all share.
//
// # Validate and autofix
//
// If the destination struct or any exported nested field implements
// [Validator], [Load] calls each one after unmarshalling (children first,
// then parent), including elements of slices, arrays, and maps of structs.
// With [WithAutoFix] the call uses fix=true so the struct can repair itself;
// otherwise it reports only. Re-run the same walk after env and flags with
// [Validate] (pass [WithFormat] to keep path prefixes aligned with the file).
//
//	func (c *Config) Validate(fix bool) []error {
//	    if c.Port <= 0 {
//	        if fix { c.Port = 8080 }
//	        return []error{fmt.Errorf("port must be > 0")}
//	    }
//	    return nil
//	}
//
// # Format detection
//
// The format is detected from the file extension (.yaml/.yml, .json, .toml).
// Use [WithFormat] to override, or [Parse]/[Marshal] to work with bytes when
// there is no extension to inspect.
//
// # Testing
//
// Inject file I/O with [WithReader] and [WithWriter] to avoid touching the
// real filesystem, or use [Parse] and [Marshal] directly:
//
//	data := []byte(`{"port": 9090}`)
//	cfgx.Parse(data, &cfg, cfgx.WithFormat(cfgx.FormatJSON))
//
// # Dependencies
//
// cfgx depends on gopkg.in/yaml.v3 and github.com/BurntSushi/toml for the
// YAML and TOML codecs; JSON uses the standard library. It imports no other
// urx subpackage.
package cfgx

import (
	"errors"
	"io/fs"
	"os"
	"reflect"
)

// Load reads a config file at path into dst and applies the nested
// [Validator] walk. The format is detected from the file extension unless
// overridden with [WithFormat].
//
// If the file does not exist and [WithCreateIfMissing] is set, dst is
// walked for [Validator]s and then written to disk in the resolved format —
// persisting corrected defaults. Remaining validation errors abort the write
// even when [WithAutoFix] repaired some fields in memory. Without
// [WithCreateIfMissing] a missing file returns [ErrNotFound]. The default
// writer creates missing parent directories.
//
// Errors: [ErrInvalidInput] (dst not a non-nil pointer), [ErrUnsupportedFormat]
// (unknown extension), [ErrNotFound], [ErrReadFailed], [ErrParseFailed],
// [ErrWriteFailed], and validation errors joined under [ErrValidationFailed].
func Load(path string, dst any, opts ...Option) error {
	if err := requirePointer("dst", dst); err != nil {
		return err
	}

	cfg := newConfig(opts)

	format, err := resolveFormat(cfg.format, path)
	if err != nil {
		return err
	}

	data, readErr := cfg.reader(path)
	if readErr != nil {
		return loadMissing(path, dst, format, &cfg, readErr)
	}

	if unmarshalErr := unmarshal(data, dst, format); unmarshalErr != nil {
		return errParseFailed(path, unmarshalErr)
	}

	return runValidator(path, dst, cfg.autoFix, format)
}

// loadMissing handles the read-error branch of [Load]: it either creates the
// file from defaults (when [WithCreateIfMissing] is set) or maps the OS error
// to a sentinel.
func loadMissing(path string, dst any, format Format, cfg *config, readErr error) error {
	if errors.Is(readErr, fs.ErrNotExist) && cfg.createOK {
		if err := runValidator(path, dst, cfg.autoFix, format); err != nil {
			return err
		}
		return save(path, dst, format, cfg.fileMode, cfg.writer)
	}
	if errors.Is(readErr, fs.ErrNotExist) {
		return errNotFound(path)
	}
	return errReadFailed(path, readErr)
}

// Parse decodes data into dst without touching the filesystem and applies the
// nested [Validator] walk. The format must be explicit via [WithFormat];
// passing [FormatAuto] (the default) returns [ErrUnsupportedFormat] because
// there is no path to infer it from.
//
// Parse is the in-memory counterpart of [Load]: ideal for embedded defaults,
// network-sourced config, and tests.
func Parse(data []byte, dst any, opts ...Option) error {
	if err := requirePointer("dst", dst); err != nil {
		return err
	}

	cfg := newConfig(opts)
	if err := requireExplicitFormat(cfg.format, pathBytes); err != nil {
		return err
	}

	if unmarshalErr := unmarshal(data, dst, cfg.format); unmarshalErr != nil {
		return errParseFailed(pathBytes, unmarshalErr)
	}

	return runValidator(pathBytes, dst, cfg.autoFix, cfg.format)
}

// Save writes src to path in the format detected from the extension (or
// forced with [WithFormat]). Useful after Validate(fix=true) to persist
// auto-corrected values.
//
// Errors: [ErrInvalidInput] (src nil), [ErrUnsupportedFormat], [ErrWriteFailed].
func Save(path string, src any, opts ...Option) error {
	if src == nil {
		return errInvalidInput("src", "must be non-nil")
	}
	rv := reflect.ValueOf(src)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return errInvalidInput("src", "must be non-nil")
	}

	cfg := newConfig(opts)

	format, err := resolveFormat(cfg.format, path)
	if err != nil {
		return err
	}

	return save(path, src, format, cfg.fileMode, cfg.writer)
}

// Marshal encodes src to bytes in the given format. The format must be
// explicit ([FormatYAML], [FormatJSON], or [FormatTOML]); [FormatAuto]
// returns [ErrUnsupportedFormat]. A nil src, including a typed nil pointer,
// returns [ErrInvalidInput]. Marshal is the in-memory counterpart of [Save].
func Marshal(src any, format Format) ([]byte, error) {
	if src == nil {
		return nil, errInvalidInput("src", "must be non-nil")
	}
	rv := reflect.ValueOf(src)
	if rv.Kind() == reflect.Pointer && rv.IsNil() {
		return nil, errInvalidInput("src", "must be non-nil")
	}
	if err := requireExplicitFormat(format, pathBytes); err != nil {
		return nil, err
	}
	data, err := marshal(src, format)
	if err != nil {
		return nil, errWriteFailed(pathBytes, err)
	}
	return data, nil
}

// save marshals src and writes it via writer, wrapping any failure in
// [ErrWriteFailed].
func save(path string, src any, format Format, mode os.FileMode, writer func(string, []byte, os.FileMode) error) error {
	data, err := marshal(src, format)
	if err != nil {
		return errWriteFailed(path, err)
	}
	if writeErr := writer(path, data, mode); writeErr != nil {
		return errWriteFailed(path, writeErr)
	}
	return nil
}

// requirePointer returns [ErrInvalidInput] unless v is a non-nil pointer.
func requirePointer(param string, v any) error {
	if v == nil {
		return errInvalidInput(param, "must be a non-nil pointer")
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errInvalidInput(param, "must be a non-nil pointer")
	}
	return nil
}
