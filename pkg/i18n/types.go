package i18n

// --- Core types ---

// Locale represents a BCP-47 language tag (e.g. "en", "ru").
type Locale string

// Anchor identifies a group of equivalent phrases that share the same meaning
// across all languages. In YAML dictionaries the anchor is the key.
type Anchor string

// Phrase is the textual representation bound to an [Anchor] in a concrete
// language dictionary.
type Phrase string

// --- Internal lookup types ---

// phraseSet provides O(1) lookup for phrase deduplication.
type phraseSet map[Phrase]struct{}

// anchorSet provides O(1) lookup for anchor deduplication.
type anchorSet map[Anchor]struct{}

// cacheKey is used as key for translation cache (language + anchor).
type cacheKey struct {
	lang   Locale
	anchor Anchor
}

// duplicates is a helper structure used only when DevMode=true to dump
// duplicate anchors/phrases for easier maintenance.
type duplicates struct {
	Anchors map[Phrase][]Anchor `yaml:"duplicated_anchors"`
	Phrases map[Anchor][]Phrase `yaml:"duplicated_phrases"`
}

// --- Statistics ---

// Stats contains usage statistics for the [Translator].
type Stats struct {
	Anchors      int `json:"anchors"`      // Number of registered anchors
	Languages    int `json:"languages"`    // Number of loaded languages
	CacheSize    int `json:"cache_size"`   // Number of cached translations
	Translations int `json:"translations"` // Total number of translations across all languages
}
