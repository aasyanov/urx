# i18n

File-based internationalization with YAML dictionaries, anchor-based lookups,
`errx` error translation, and per-string caching.

## Philosophy

**One job: translate strings.** `i18n` loads YAML dictionary files, resolves
anchors by dot-separated keys, formats arguments with `fmt.Sprintf`, and caches
results. `TranslateError` bridges `errx.Error` to user-facing messages. It does
not manage locales per request, handle pluralization rules, or generate
translation files.

## Quick start

```go
tr := i18n.New()
tr.NewPhrase("GREETING", "Привет")
tr.NewPhrase("WITH_ARG", "Привет, %s!")

tr.Init(
    i18n.WithFolder("./lang"),
    i18n.WithLanguage("en"),
)

msg := tr.T("GREETING")              // "Hello" (from en.yaml)
msg  = tr.T("WITH_ARG", "World")     // "Hello, World!"
msg  = tr.T2("ru", "GREETING")       // "Привет" (explicit locale)
```

### errx bridge

```go
xe := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "token expired")
msg := tr.TranslateError("en", xe)   // looks up "AUTH.UNAUTHORIZED" anchor
```

### Global API (slog pattern)

```go
i18n.Init(i18n.WithFolder("./lang"), i18n.WithLanguage("en"))
msg := i18n.T("GREETING")
```

## API

### Translator

| Method | Description |
|---|---|
| `New() *Translator` | Create a translator with safe defaults |
| `t.Init(opts ...Option) []error` | Load dictionaries, freeze phrase registry |
| `t.MustInit(opts ...Option)` | Init or panic on first error |
| `t.Reload(opts ...Option) []error` | Reload dictionaries from disk |
| `t.NewPhrase(anchor, phrase) Phrase` | Register an inline phrase (no-op after Init) |
| `t.T(anchor, args...) string` | Translate using current language |
| `t.T2(lang, anchor, args...) string` | Translate using explicit language |
| `t.TranslateAnchor(lang, anchor, args...) string` | Direct anchor lookup with explicit language |
| `t.TranslateError(lang, err) string` | Translate `*errx.Error` by `DOMAIN.CODE` anchor |
| `t.Language() Locale` | Current active language |
| `t.SetLanguage(lang) Locale` | Switch language; returns language after call |
| `t.Languages() []Locale` | All loaded language tags (copy) |
| `t.HasLanguage(lang) bool` | Whether language dictionary is loaded |
| `t.HasTranslation(lang, anchor) bool` | Whether anchor exists in language |
| `t.Anchors() []Anchor` | All registered anchor keys |
| `t.Folder() string` | Dictionary folder path |
| `t.FileExt() string` | Dictionary file extension |
| `t.Frozen() bool` | Whether phrase registry is frozen (after Init) |
| `t.Stats() Stats` | Anchor count, language count, cache size, translations |
| `t.ClearCache()` | Invalidate translation cache |
| `t.SaveDefault(filename) error` | Write default dictionary YAML to disk |

### Global functions

| Function | Description |
|---|---|
| `Default() *Translator` | Package-level translator singleton |
| `SetDefault(t)` | Replace default translator (panics if nil) |
| `Init(opts...) []error` | Initialize default translator |
| `MustInit(opts...)` | Init or panic |
| `Reload(opts...) []error` | Reload default translator |
| `NewPhrase(anchor, phrase) Phrase` | Register phrase on default translator |
| `T(anchor, args...) string` | Translate via default |
| `T2(lang, anchor, args...) string` | Translate with explicit language via default |
| `TranslateAnchor(lang, anchor, args...) string` | Direct lookup via default |
| `TranslateError(lang, err) string` | Translate error via default |
| `Language() Locale` | Active language of default |
| `SetLanguage(lang) Locale` | Switch language on default |
| `Languages() []Locale` | Loaded languages of default |
| `Folder() string` | Folder of default |
| `FileExt() string` | Extension of default |
| `Frozen() bool` | Frozen state of default |
| `HasLanguage(lang) bool` | Language check on default |
| `HasTranslation(lang, anchor) bool` | Translation check on default |
| `Anchors() []Anchor` | Anchors of default |
| `GetStats() Stats` | Stats of default (named GetStats to avoid type collision) |
| `ClearCache()` | Clear cache of default |
| `SaveDefault(filename) error` | Save default dictionary of default |

### Options

| Option | Default | Description |
|---|---|---|
| `WithFolder(path)` | `"./language"` | Dictionary folder |
| `WithFileExt(ext)` | `".yaml"` | File extension |
| `WithLanguage(lang)` | `"ru"` | Active language after Init |
| `WithDevMode()` | off | Enable auto-sync of YAML files and duplicate detection |

## Behavior details

- **Dictionary loading**: `Init` scans the folder for `*<ext>` files. Each file
  is named by language tag (`en.yaml`, `ru.yaml`). The default-language file
  (`ru.yaml`) is skipped — its phrases come from `NewPhrase` registrations.
  Files starting with `_` and subdirectories are skipped.

- **Anchor resolution**: `T("GREETING")` looks up the anchor in the current
  language dictionary, falls back to the default language if the language is
  missing, and returns the anchor name as-is if no translation exists anywhere.

- **Formatting**: when `args` are provided, `fmt.Sprintf` is applied. If an
  argument is of type `Anchor`, it is recursively translated first.

- **Caching**: translations **without arguments** are cached per
  `(language, anchor)` in a `sync.Map`. Translations with arguments bypass the
  cache (each call runs `fmt.Sprintf`). `ClearCache` resets the cache.
  `Init` and `Reload` also clear it.

- **TranslateError**: for `*errx.Error`, builds an anchor as `Domain + "." + Code`
  (e.g. `AUTH.UNAUTHORIZED`) and looks it up. Falls back to `errx.Error.Message`
  if no translation exists. For plain `error` returns `err.Error()`. Nil returns `""`.

- **Phrase registry freeze**: after `Init`, `NewPhrase` becomes a no-op. Only the
  first phrase per anchor is stored; duplicates are tracked for DevMode detection.

- **DevMode auto-sync**: when `WithDevMode()` is set, `Init`/`Reload` synchronise
  YAML files on disk — missing anchors are added with default-language values,
  stale anchors are removed. A `_duplicates.yaml` report is written when the same
  phrase maps to multiple anchors or vice versa. **Without DevMode, files are
  read-only** — no disk writes occur during Init/Reload.

- **Fallback chain**: target language → default language → anchor name as-is.

## Thread safety

- `T` / `T2` / `TranslateAnchor` — `sync.RWMutex` read lock for dictionary, `sync.Map` for cache
- `TranslateError` — delegates to `TranslateAnchor`, same guarantees
- `SetLanguage` — `sync.Mutex` write lock
- `Init` / `Reload` — `sync.Mutex` write lock, clears and rebuilds state
- `NewPhrase` — `sync.Mutex` write lock (no-op after freeze)
- `ClearCache` — iterates and deletes all entries via `sync.Map.Range`/`Delete` (safe for concurrent readers)
- Global functions — delegate through `atomic.Pointer[Translator]`

## Tests

**74 tests, 97.5% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/i18n  coverage: 97.5% of statements
```

Coverage includes:
- Init: valid folder, missing folder, invalid YAML, multiple languages, cache invalidation
- MustInit: panics on error, no panic on success
- Reload: file changes reflected, cache cleared
- T: cached hit, uncached, with args, missing anchor
- T2: explicit language lookup
- TranslateAnchor: cached, miss (returns anchor name), fallback to default language
- TranslateError: errx.Error, wrapped errx.Error, plain error, nil, fallback to Message
- NewPhrase: registration, duplicate anchor (first wins), frozen state (no-op)
- SetLanguage: switch, unavailable language (unchanged)
- Languages / HasLanguage / HasTranslation: loaded and missing
- Stats: anchor count, language count, translation count, cache size
- ClearCache: clears populated cache
- SaveDefault: writes YAML file, invalid folder error
- DevMode: auto-sync adds missing anchors, no sync without DevMode
- DevMode: duplicate detection writes _duplicates.yaml, no file when clean
- formatString: error args, Anchor args, multiple args, no args
- Global API: Init, T, T2, TranslateAnchor, TranslateError, Reload, NewPhrase,
  SetLanguage, Languages, QueryHelpers, ClearCache, SaveDefault, SetDefault
- Concurrency: 100 goroutines calling T/T2/TranslateAnchor/TranslateError/Stats
- Concurrency: ClearCache concurrent with T (race-free)
- SetDefault: nil panics
- Edge cases: skips dirs, underscore files, wrong extension, default lang file,
  unreadable files, invalid folder paths

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkT_Cached                      ~61 ns/op       0 B/op     0 allocs/op
BenchmarkT_Uncached                   ~621 ns/op     256 B/op     4 allocs/op
BenchmarkT2                            ~59 ns/op       0 B/op     0 allocs/op
BenchmarkT_WithArgs                   ~251 ns/op      32 B/op     2 allocs/op
BenchmarkTranslateAnchor_Cached        ~51 ns/op       0 B/op     0 allocs/op
BenchmarkTranslateAnchor_Miss          ~65 ns/op       0 B/op     0 allocs/op
BenchmarkTranslateError               ~360 ns/op      32 B/op     2 allocs/op
BenchmarkTranslateError_Fallback      ~499 ns/op      24 B/op     2 allocs/op
BenchmarkTranslateError_PlainError    ~250 ns/op       8 B/op     1 allocs/op
BenchmarkNewPhrase                   ~1828 ns/op    2160 B/op    16 allocs/op
BenchmarkInit                       ~287000 ns/op  14600 B/op   100 allocs/op
BenchmarkSetLanguage                    ~59 ns/op       0 B/op     0 allocs/op
BenchmarkStats                         ~100 ns/op       0 B/op     0 allocs/op
BenchmarkGlobal_T                       ~74 ns/op       0 B/op     0 allocs/op
```

### Analysis

**T (cached):** ~61 ns, 0 allocs. RWMutex read lock + `sync.Map` load. Hot-path cost.

**T (uncached):** ~621 ns, 4 allocs. RWMutex read lock + dictionary lookup + `sync.Map` store. First-time cost per `(language, anchor)` pair.

**T2:** ~59 ns. Same as T but skips the RWMutex read for `t.lang` — language is passed directly.

**TranslateAnchor (cached):** ~51 ns. Fastest path — no language field read.

**TranslateAnchor (miss):** ~65 ns, 0 allocs. Dictionary lookup returns anchor name, no cache store.

**TranslateError:** ~360 ns. `errors.As` + anchor construction + `TranslateAnchor`.

**Init:** ~287 us, 100 allocs. File I/O + YAML parsing. One-time startup cost. Without DevMode, no marshal/write-back overhead.

**SetLanguage:** ~59 ns. Write lock + `slices.Contains` check. Free.

**Global T:** ~74 ns. `atomic.Pointer` load + T call. ~13 ns overhead over direct T.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| T (cached) | < 65 ns, 0 allocs | Hot-path friendly |
| T (uncached) | < 650 ns, 4 allocs | First-time per anchor |
| TranslateError | < 400 ns, 2 allocs | Fast error bridge |
| Init | < 300 us, 100 allocs | One-time startup |
| SetLanguage | < 60 ns | Free |

## What i18n does NOT do

| Concern | Owner |
|---------|-------|
| Per-request locale | HTTP middleware / caller |
| Pluralization rules | caller / ICU library |
| ICU MessageFormat | caller |
| Translation management | Crowdin / Transifex |
| Config loading | caller |

## File structure

```text
pkg/i18n/
    translator.go  -- Translator, New(), Init(), T(), TranslateError(), formatString()
    config.go      -- Option, WithFolder(), WithFileExt(), WithLanguage(), WithDevMode()
    global.go      -- Default(), SetDefault(), global forwarding functions
    types.go       -- Locale, Anchor, Phrase, Stats, internal lookup types
    i18n_test.go   -- 74 tests, 97.5% coverage
    bench_test.go  -- 14 benchmarks
    README.md
```
