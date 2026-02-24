package i18n

import "sync/atomic"

// --- Default values ---

const (
	// DefaultLang is the fallback locale used when none is specified.
	DefaultLang Locale = "ru"
	// DefaultFolder is the default directory for translation files.
	DefaultFolder string = "./language"
	// DefaultFileExt is the default file extension for translation files.
	DefaultFileExt string = ".yaml"
)

// --- Default translator (slog pattern) ---

// defaultTranslator holds the package-level [Translator] singleton (slog pattern).
var defaultTranslator atomic.Pointer[Translator]

func init() { defaultTranslator.Store(New()) }

// Default returns the package-level [Translator] instance.
func Default() *Translator { return defaultTranslator.Load() }

// SetDefault replaces the package-level [Translator].
// Subsequent calls to the global functions ([T], [T2], [Init], etc.)
// will delegate to t. Panics if t is nil.
func SetDefault(t *Translator) {
	if t == nil {
		panic("i18n: SetDefault translator must not be nil")
	}
	defaultTranslator.Store(t)
}

// --- Global wrappers ---

// Init initialises the default translator. See [Translator.Init].
func Init(opts ...Option) []error { return Default().Init(opts...) }

// MustInit calls [Init] and panics on error.
func MustInit(opts ...Option) { Default().MustInit(opts...) }

// Reload reloads dictionaries of the default translator. See [Translator.Reload].
func Reload(opts ...Option) []error { return Default().Reload(opts...) }

// NewPhrase registers a phrase on the default translator. See [Translator.NewPhrase].
func NewPhrase(anchor Anchor, phrase Phrase) Phrase { return Default().NewPhrase(anchor, phrase) }

// T translates an anchor using the active language of the default translator.
func T(anchor Anchor, args ...any) string { return Default().T(anchor, args...) }

// T2 translates an anchor using the supplied language on the default translator.
func T2(lang Locale, anchor Anchor, args ...any) string { return Default().T2(lang, anchor, args...) }

// TranslateAnchor performs a direct anchor lookup on the default translator.
func TranslateAnchor(lang Locale, anchor Anchor, args ...any) string {
	return Default().TranslateAnchor(lang, anchor, args...)
}

// TranslateError translates an [errx.Error] via the default translator.
// See [Translator.TranslateError].
func TranslateError(lang Locale, err error) string { return Default().TranslateError(lang, err) }

// Language returns the active language of the default translator.
func Language() Locale { return Default().Language() }

// SetLanguage switches the active language on the default translator.
func SetLanguage(required Locale) Locale { return Default().SetLanguage(required) }

// Languages returns all loaded languages of the default translator.
func Languages() []Locale { return Default().Languages() }

// Folder returns the translation directory of the default translator.
func Folder() string { return Default().Folder() }

// FileExt returns the file extension of the default translator.
func FileExt() string { return Default().FileExt() }

// Frozen reports whether the default translator's registry is frozen.
func Frozen() bool { return Default().Frozen() }

// HasLanguage reports whether the default translator has a dictionary for lang.
func HasLanguage(lang Locale) bool { return Default().HasLanguage(lang) }

// HasTranslation reports whether the default translator has a translation for anchor in lang.
func HasTranslation(lang Locale, anchor Anchor) bool { return Default().HasTranslation(lang, anchor) }

// Anchors returns all registered anchors of the default translator.
func Anchors() []Anchor { return Default().Anchors() }

// GetStats returns statistics of the default translator.
// Named GetStats (not Stats) to avoid colliding with the [Stats] type.
func GetStats() Stats { return Default().Stats() }

// ClearCache invalidates the translation cache of the default translator.
func ClearCache() { Default().ClearCache() }

// SaveDefault writes the default dictionary to disk via the default translator.
func SaveDefault(filename string) error { return Default().SaveDefault(filename) }
