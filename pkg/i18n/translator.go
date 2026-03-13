// Package i18n provides a thread-safe, anchor-based translation system with
// YAML dictionary loading, per-language fallback, and a zero-switch bridge
// to [errx.Error] via Domain.Code anchors.
//
// The central type is [Translator]. A package-level default instance is
// available through [Default] / [SetDefault] (following the slog pattern),
// and thin global wrappers ([T], [T2], [Init], etc.) delegate to it.
//
// Typical usage:
//
//	i18n.Init(i18n.WithFolder("./lang"), i18n.WithLanguage("en"))
//	msg := i18n.T("GREETING")                       // anchor lookup
//	msg  = i18n.T2("ru", "GREETING")                // explicit locale
//	msg  = i18n.TranslateError("en", someErrxError)  // errx bridge
//
package i18n

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/aasyanov/urx/pkg/errx"
	"gopkg.in/yaml.v3"
)

// --- Translator ---

// Translator holds all runtime state of the i18n subsystem: dictionaries,
// caches, and configuration. All exported methods are safe for concurrent use.
//
// Create instances with [New]. A package-level default is accessible via
// [Default] and can be replaced with [SetDefault].
type Translator struct {
	dir     string
	ext     string
	lang    Locale
	mu      sync.RWMutex
	langs   []Locale
	dict    map[Locale]map[Anchor]Phrase
	phrases map[Anchor]phraseSet
	anchors map[Phrase]anchorSet
	primary map[Anchor]Phrase
	frozen  bool
	cache   sync.Map
}

// New creates a ready-to-use [Translator] populated with safe defaults.
func New() *Translator {
	return &Translator{
		dir:     DefaultFolder,
		ext:     DefaultFileExt,
		lang:    DefaultLang,
		langs:   []Locale{DefaultLang},
		dict:    map[Locale]map[Anchor]Phrase{DefaultLang: {}},
		phrases: make(map[Anchor]phraseSet),
		anchors: make(map[Phrase]anchorSet),
		primary: make(map[Anchor]Phrase),
	}
}

// --- Lifecycle ---

// Init initialises the translator with the provided options applied on top
// of the default configuration. The first call freezes the phrase registry so that
// subsequent [Translator.NewPhrase] calls are ignored. Returns a slice of
// non-fatal errors encountered during dictionary processing (e.g. malformed
// YAML files).
func (t *Translator) Init(opts ...Option) []error {
	c := defaultConfig()
	for _, opt := range opts {
		opt(&c)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.resetCache()

	t.dict = make(map[Locale]map[Anchor]Phrase)
	t.dict[DefaultLang] = make(map[Anchor]Phrase)
	for anchor, phrase := range t.primary {
		t.dict[DefaultLang][anchor] = phrase
	}

	errs := t.processDictionaries(&c)
	t.frozen = true
	return errs
}

// MustInit calls [Translator.Init] and panics when any error is returned.
func (t *Translator) MustInit(opts ...Option) {
	if errs := t.Init(opts...); len(errs) > 0 {
		panic("i18n.MustInit: " + errs[0].Error())
	}
}

// Reload reloads all dictionaries from disk. The phrase registry stays frozen.
// Safe to call concurrently with ongoing translations.
func (t *Translator) Reload(opts ...Option) []error {
	c := defaultConfig()
	for _, opt := range opts {
		opt(&c)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	t.resetCache()

	t.dict = make(map[Locale]map[Anchor]Phrase)
	t.dict[DefaultLang] = make(map[Anchor]Phrase)
	for anchor, phrase := range t.primary {
		t.dict[DefaultLang][anchor] = phrase
	}

	return t.processDictionaries(&c)
}

// --- Phrase registration ---

// NewPhrase registers a phrase with its anchor in the default language
// dictionary. Only the first phrase per anchor is stored; duplicates are
// tracked for DevMode detection but do not override. After [Translator.Init]
// the registry is frozen and NewPhrase becomes a no-op.
// Returns phrase unchanged for convenient inline usage.
func (t *Translator) NewPhrase(anchor Anchor, phrase Phrase) Phrase {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.frozen {
		return phrase
	}

	if _, ok := t.primary[anchor]; !ok {
		t.primary[anchor] = phrase
		t.dict[DefaultLang][anchor] = phrase
	}

	if t.phrases[anchor] == nil {
		t.phrases[anchor] = make(phraseSet)
	}
	t.phrases[anchor][phrase] = struct{}{}

	if t.anchors[phrase] == nil {
		t.anchors[phrase] = make(anchorSet)
	}
	t.anchors[phrase][anchor] = struct{}{}

	return phrase
}

// --- Translation ---

// T translates an anchor using the currently active language.
func (t *Translator) T(anchor Anchor, args ...any) string {
	t.mu.RLock()
	lang := t.lang
	t.mu.RUnlock()
	return t.TranslateAnchor(lang, anchor, args...)
}

// T2 translates an anchor using the supplied language tag.
func (t *Translator) T2(lang Locale, anchor Anchor, args ...any) string {
	return t.TranslateAnchor(lang, anchor, args...)
}

// TranslateAnchor performs a direct anchor lookup without phrase-to-anchor
// indirection. Falls back to the default language when the target dictionary
// is missing. Returns the anchor name as-is when no translation exists.
func (t *Translator) TranslateAnchor(lang Locale, anchor Anchor, args ...any) string {
	noArgs := len(args) == 0
	if noArgs {
		key := cacheKey{lang: lang, anchor: anchor}
		if cached, ok := t.cache.Load(key); ok {
			return cached.(string)
		}
	}

	t.mu.RLock()
	dict, ok := t.dict[lang]
	if !ok {
		dict = t.dict[DefaultLang]
	}
	phrase, found := dict[anchor]
	t.mu.RUnlock()

	if !found {
		return string(anchor)
	}

	result := string(phrase)

	if noArgs {
		key := cacheKey{lang: lang, anchor: anchor}
		t.cache.Store(key, result)
		return result
	}

	return t.formatString(result, args...)
}

// --- errx bridge ---

// TranslateError translates an [errx.Error] by building an anchor from
// Domain + "." + Code (e.g. "AUTH.UNAUTHORIZED") and looking it up in the
// dictionary. When no translation is found, [errx.Error.Message] is returned
// as a graceful fallback. For plain errors the result of Error() is returned.
func (t *Translator) TranslateError(lang Locale, err error) string {
	if err == nil {
		return ""
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		return err.Error()
	}
	anchor := Anchor(xe.Domain + "." + xe.Code)
	msg := t.TranslateAnchor(lang, anchor)
	if msg == string(anchor) {
		return xe.Message
	}
	return msg
}

// --- Language management ---

// Language returns the currently active language tag.
func (t *Translator) Language() Locale {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lang
}

// SetLanguage switches the active language if the requested locale has a
// loaded dictionary. Returns the language that is set after the call.
func (t *Translator) SetLanguage(required Locale) Locale {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.setLanguage(required)
}

// Languages returns a copy of all loaded language tags.
func (t *Translator) Languages() []Locale {
	t.mu.RLock()
	defer t.mu.RUnlock()
	cp := make([]Locale, len(t.langs))
	copy(cp, t.langs)
	return cp
}

// --- Query helpers ---

// Folder returns the directory where translation files are stored.
func (t *Translator) Folder() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.dir
}

// FileExt returns the extension used for translation files.
func (t *Translator) FileExt() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.ext
}

// Frozen reports whether the phrase registry has been frozen.
func (t *Translator) Frozen() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.frozen
}

// HasLanguage reports whether a dictionary for lang is loaded.
func (t *Translator) HasLanguage(lang Locale) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.dict[lang]
	return ok
}

// HasTranslation reports whether anchor has a translation for lang.
func (t *Translator) HasTranslation(lang Locale, anchor Anchor) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if dict, ok := t.dict[lang]; ok {
		_, exists := dict[anchor]
		return exists
	}
	return false
}

// Anchors returns all registered anchor keys.
func (t *Translator) Anchors() []Anchor {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]Anchor, 0, len(t.primary))
	for a := range t.primary {
		result = append(result, a)
	}
	return result
}

// Stats returns current usage statistics.
func (t *Translator) Stats() Stats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	cacheSize := 0
	t.cache.Range(func(_, _ any) bool {
		cacheSize++
		return true
	})

	translations := 0
	for _, dict := range t.dict {
		translations += len(dict)
	}

	return Stats{
		Anchors:      len(t.primary),
		Languages:    len(t.langs),
		CacheSize:    cacheSize,
		Translations: translations,
	}
}

// ClearCache invalidates the translation cache. Thread-safe.
func (t *Translator) ClearCache() {
	t.resetCache()
}

func (t *Translator) resetCache() {
	t.cache.Range(func(key, _ any) bool {
		t.cache.Delete(key)
		return true
	})
}

// SaveDefault writes the default language dictionary to a YAML file on disk.
func (t *Translator) SaveDefault(filename string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.saveDefault(t.dir, filename, t.ext)
}

// --- Internal: dictionary loading ---

// saveDefault writes the default dictionary YAML file for the active language.
func (t *Translator) saveDefault(folder, name, ext string) error {
	if err := os.MkdirAll(folder, 0755); err != nil {
		return err
	}
	blob, err := yaml.Marshal(t.dict[DefaultLang])
	if err != nil {
		return err
	}
	path := filepath.Join(folder, strings.TrimSuffix(filepath.Base(name), ext)+ext)
	return os.WriteFile(path, blob, 0644)
}

// setLanguage switches the active language and returns the resulting locale.
func (t *Translator) setLanguage(required Locale) Locale {
	if slices.Contains(t.langs, required) {
		t.lang = required
	}
	return t.lang
}

// processDictionaries loads all translation YAML files. In DevMode it also
// synchronises files on disk (adding missing anchors, removing stale ones)
// and writes a _duplicates.yaml report. Caller must hold t.mu write lock.
func (t *Translator) processDictionaries(c *config) (errs []error) {
	t.dir = c.folder
	t.ext = c.fileExt

	if err := os.MkdirAll(t.dir, 0755); err != nil {
		errs = append(errs, err)
	}

	files, err := os.ReadDir(t.dir)
	if err != nil {
		errs = append(errs, err)
		return
	}

	for _, file := range files {
		name := file.Name()
		path := filepath.Join(t.dir, name)

		if file.IsDir() || strings.HasPrefix(name, "_") || !strings.HasSuffix(name, t.ext) {
			continue
		}

		lang := Locale(strings.ReplaceAll(filepath.Base(name), filepath.Ext(name), ""))
		if lang == DefaultLang {
			continue
		}

		srcBlob, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		dict := make(map[Anchor]Phrase)
		if err := yaml.Unmarshal(srcBlob, &dict); err != nil {
			errs = append(errs, err)
			continue
		}

		t.applyDictionary(lang, dict)

		if c.devMode {
			dstBlob, err := yaml.Marshal(t.dict[lang])
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !bytes.Equal(srcBlob, dstBlob) {
				if err := os.WriteFile(path, dstBlob, 0644); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}

	t.langs = make([]Locale, 0, len(t.dict))
	for k := range t.dict {
		t.langs = append(t.langs, k)
	}

	t.setLanguage(c.language)

	if c.devMode {
		t.detectDuplicates()
	}

	return
}

// applyDictionary merges a loaded dictionary into the translator state.
// Keeps only anchors present in the default dictionary and fills gaps with
// default-language values.
func (t *Translator) applyDictionary(lang Locale, loaded map[Anchor]Phrase) {
	t.dict[lang] = make(map[Anchor]Phrase)

	for anchor, phrase := range loaded {
		if _, ok := t.dict[DefaultLang][anchor]; ok {
			t.dict[lang][anchor] = phrase
		}
	}

	for anchor, phrase := range t.dict[DefaultLang] {
		if _, ok := loaded[anchor]; !ok {
			t.dict[lang][anchor] = phrase
		}
	}
}

// detectDuplicates writes a _duplicates.yaml file in DevMode.
func (t *Translator) detectDuplicates() {
	dups := &duplicates{
		Anchors: make(map[Phrase][]Anchor),
		Phrases: make(map[Anchor][]Phrase),
	}

	for anchor, phrases := range t.phrases {
		if len(phrases) > 1 {
			list := make([]Phrase, 0, len(phrases))
			for p := range phrases {
				list = append(list, p)
			}
			dups.Phrases[anchor] = list
		}
		for phrase := range phrases {
			dups.Anchors[phrase] = append(dups.Anchors[phrase], anchor)
		}
	}

	for phrase, anchors := range dups.Anchors {
		if len(anchors) <= 1 {
			delete(dups.Anchors, phrase)
		}
	}

	if len(dups.Anchors) > 0 || len(dups.Phrases) > 0 {
		if blob, err := yaml.Marshal(dups); err == nil {
			_ = os.WriteFile(filepath.Join(t.dir, "_duplicates.yaml"), blob, 0644)
		}
	}
}

// --- Internal: formatting ---

// formatString applies fmt.Sprintf to the format string. If any argument is
// an [Anchor], it is recursively translated before formatting.
func (t *Translator) formatString(format string, args ...any) string {
	translatedArgs := make([]any, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case Anchor:
			translatedArgs[i] = t.TranslateAnchor(t.Language(), v)
		default:
			translatedArgs[i] = v
		}
	}
	return fmt.Sprintf(format, translatedArgs...)
}
