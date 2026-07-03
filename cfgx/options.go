package cfgx

import "os"

// defaultFileMode is the permission applied to files created by [Save] and
// by [Load] with [WithCreateIfMissing]. Override with [WithFileMode].
const defaultFileMode os.FileMode = 0o644

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
		writer:   os.WriteFile,
	}
}

// Option configures [Load] or [Save] behavior.
type Option func(*config)

// WithFormat forces a specific format instead of auto-detecting from the
// file extension. Default: [FormatAuto] (detect from extension).
func WithFormat(f Format) Option {
	return func(c *config) { c.format = f }
}

// WithAutoFix enables automatic fixing when the destination struct
// implements [Validator]. Without this option, Validate is called with
// fix=false (report only). Default: disabled. Any validation errors that
// remain after the fix pass are propagated by [Load].
func WithAutoFix() Option {
	return func(c *config) { c.autoFix = true }
}

// WithCreateIfMissing makes [Load] write the destination struct (with its
// current default values) to disk when the file does not exist, instead of
// returning [ErrNotFound]. Default: disabled.
func WithCreateIfMissing() Option {
	return func(c *config) { c.createOK = true }
}

// WithFileMode sets the permission bits used when creating files via [Save]
// or [Load] with [WithCreateIfMissing]. Default: 0o644.
func WithFileMode(mode os.FileMode) Option {
	return func(c *config) { c.fileMode = mode }
}

// WithReader replaces the default file reader ([os.ReadFile]). A nil
// function is ignored. Useful for testing without touching the filesystem.
func WithReader(fn func(path string) ([]byte, error)) Option {
	return func(c *config) {
		if fn != nil {
			c.reader = fn
		}
	}
}

// WithWriter replaces the default file writer ([os.WriteFile]). A nil
// function is ignored. Useful for testing without touching the filesystem.
func WithWriter(fn func(path string, data []byte, perm os.FileMode) error) Option {
	return func(c *config) {
		if fn != nil {
			c.writer = fn
		}
	}
}
