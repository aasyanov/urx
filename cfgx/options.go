package cfgx

import (
	"os"
	"path/filepath"
)

// defaultFileMode is the permission applied to files created by [Save] and
// by [Load] with [WithCreateIfMissing]. Override with [WithFileMode].
const defaultFileMode os.FileMode = 0o644

// defaultDirMode is used when the default writer creates missing parent
// directories. Override is not offered: inject [WithWriter] to own layout.
const defaultDirMode os.FileMode = 0o755

// pathCurrentDir is filepath.Dir of a bare filename ("config.yaml" → ".").
const pathCurrentDir = "."

type config struct {
	format   Format
	autoFix  bool
	createOK bool
	fileMode os.FileMode
	reader   func(string) ([]byte, error)
	writer   func(string, []byte, os.FileMode) error
}

func defaultConfig() config {
	return config{
		fileMode: defaultFileMode,
		reader:   os.ReadFile,
		writer:   writeFile,
	}
}

// newConfig applies opts over defaults. Nil Option values are ignored.
func newConfig(opts []Option) config {
	cfg := defaultConfig()
	for _, o := range opts {
		if o != nil {
			o(&cfg)
		}
	}
	return cfg
}

// writeFile creates missing parent directories, then writes path. Used as
// the default [WithWriter] implementation.
func writeFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != pathCurrentDir {
		if err := os.MkdirAll(dir, defaultDirMode); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, perm)
}

// Option configures [Load], [Parse], [Save], or [Validate]. Each WithXxx
// documents which operations it affects; options with no effect on an
// operation are ignored. Nil Option values are ignored.
type Option func(*config)

// WithFormat forces a specific format instead of auto-detecting from the
// file extension. Applies to [Load], [Parse], [Save], and [Validate].
// Default: [FormatAuto] (detect from extension on [Load] and [Save]; on
// [Validate], prefer yaml → json → toml struct tags).
func WithFormat(f Format) Option {
	return func(c *config) { c.format = f }
}

// WithAutoFix enables automatic fixing when the destination struct
// implements [Validator]. Applies to [Load] and [Parse]. Without this
// option, Validate is called with fix=false (report only). Default:
// disabled. Any validation errors that remain after the fix pass are
// propagated. Does not change the fix flag passed to [Validate].
func WithAutoFix() Option {
	return func(c *config) { c.autoFix = true }
}

// WithCreateIfMissing makes [Load] write the destination struct (with its
// current default values) to disk when the file does not exist, instead of
// returning [ErrNotFound]. Applies to [Load] only. Default: disabled.
// Remaining validation errors abort the write even when [WithAutoFix]
// repaired some fields in memory.
func WithCreateIfMissing() Option {
	return func(c *config) { c.createOK = true }
}

// WithFileMode sets the permission bits used when creating files via [Save]
// or [Load] with [WithCreateIfMissing]. Default: 0o644.
func WithFileMode(mode os.FileMode) Option {
	return func(c *config) { c.fileMode = mode }
}

// WithReader replaces the default file reader ([os.ReadFile]). Applies to
// [Load] only. A nil function is ignored. Useful for testing without
// touching the filesystem.
func WithReader(fn func(path string) ([]byte, error)) Option {
	return func(c *config) {
		if fn != nil {
			c.reader = fn
		}
	}
}

// WithWriter replaces the default file writer ([writeFile]: MkdirAll +
// [os.WriteFile]). Applies to [Save] and [Load] with [WithCreateIfMissing].
// A nil function is ignored. Useful for testing without touching the
// filesystem; an injected writer does not create parent directories.
func WithWriter(fn func(path string, data []byte, perm os.FileMode) error) Option {
	return func(c *config) {
		if fn != nil {
			c.writer = fn
		}
	}
}
