package i18n

// --- Options ---

// Option configures [Translator.Init] and [Translator.Reload] behavior.
type Option func(*config)

// WithFolder sets the directory where translation YAML files are located.
func WithFolder(s string) Option {
	return func(c *config) {
		if s != "" {
			c.folder = s
		}
	}
}

// WithFileExt sets the extension of translation files (e.g. ".yaml").
func WithFileExt(s string) Option {
	return func(c *config) {
		if s != "" {
			c.fileExt = s
		}
	}
}

// WithLanguage sets the initial application language.
func WithLanguage(l Locale) Option {
	return func(c *config) {
		if l != "" {
			c.language = l
		}
	}
}

// WithDevMode enables additional development checks:
//   - Duplicate phrase/anchor detection
//   - Writes _duplicates.yaml with detected issues
func WithDevMode() Option {
	return func(c *config) { c.devMode = true }
}

// --- Internal config ---

// config holds translator initialization parameters.
type config struct {
	folder   string
	fileExt  string
	language Locale
	devMode  bool
}

// defaultConfig returns a baseline configuration populated with safe defaults.
func defaultConfig() config {
	return config{
		folder:   DefaultFolder,
		fileExt:  DefaultFileExt,
		language: DefaultLang,
	}
}
