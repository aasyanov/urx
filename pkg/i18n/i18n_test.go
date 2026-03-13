package i18n

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- helpers ---

func newTestTranslator(t *testing.T) (*Translator, string) {
	t.Helper()
	dir := t.TempDir()
	tr := New()
	tr.NewPhrase("GREETING", "Привет")
	tr.NewPhrase("FAREWELL", "До свидания")
	tr.NewPhrase("WITH_ARG", "Привет, %s!")
	tr.NewPhrase("AUTH.UNAUTHORIZED", "Не авторизован")
	tr.NewPhrase("REPO.NOT_FOUND", "%s не найден")

	en := map[Anchor]Phrase{
		"GREETING":          "Hello",
		"FAREWELL":          "Goodbye",
		"WITH_ARG":          "Hello, %s!",
		"AUTH.UNAUTHORIZED": "Unauthorized",
		"REPO.NOT_FOUND":    "%s not found",
	}
	writeYAML(t, dir, en)

	return tr, dir
}

func writeYAML(t *testing.T, dir string, data map[Anchor]Phrase) {
	t.Helper()
	var buf []byte
	for k, v := range data {
		buf = append(buf, []byte(fmt.Sprintf("%q: %q\n", string(k), string(v)))...)
	}
	if err := os.WriteFile(filepath.Join(dir, "en.yaml"), buf, 0644); err != nil {
		t.Fatal(err)
	}
}

// --- New ---

func TestNew_Defaults(t *testing.T) {
	tr := New()
	if tr.Language() != DefaultLang {
		t.Errorf("Language() = %q, want %q", tr.Language(), DefaultLang)
	}
	if tr.Folder() != DefaultFolder {
		t.Errorf("Folder() = %q, want %q", tr.Folder(), DefaultFolder)
	}
	if tr.FileExt() != DefaultFileExt {
		t.Errorf("FileExt() = %q, want %q", tr.FileExt(), DefaultFileExt)
	}
	if tr.Frozen() {
		t.Error("new translator should not be frozen")
	}
}

// --- NewPhrase ---

func TestNewPhrase_Registration(t *testing.T) {
	tr := New()
	got := tr.NewPhrase("TEST", "test value")
	if got != "test value" {
		t.Errorf("NewPhrase returned %q, want %q", got, "test value")
	}
	anchors := tr.Anchors()
	if len(anchors) != 1 || anchors[0] != "TEST" {
		t.Errorf("Anchors() = %v, want [TEST]", anchors)
	}
}

func TestNewPhrase_DuplicateAnchor(t *testing.T) {
	tr := New()
	tr.NewPhrase("DUP", "first")
	tr.NewPhrase("DUP", "second")

	msg := tr.T("DUP")
	if msg != "first" {
		t.Errorf("T(DUP) = %q, want %q (first registered wins)", msg, "first")
	}
}

func TestNewPhrase_AfterFreeze(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	tr.NewPhrase("LATE", "late phrase")
	msg := tr.T("LATE")
	if msg != "LATE" {
		t.Errorf("T(LATE) = %q, want anchor name (not registered after freeze)", msg)
	}
}

// --- Init ---

func TestInit_LoadsDictionaries(t *testing.T) {
	tr, dir := newTestTranslator(t)
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	if len(errs) > 0 {
		t.Fatalf("Init errors: %v", errs)
	}

	if !tr.HasLanguage("en") {
		t.Error("expected en language to be loaded")
	}
	if tr.Language() != "en" {
		t.Errorf("Language() = %q, want %q", tr.Language(), "en")
	}
	if !tr.Frozen() {
		t.Error("expected frozen after Init")
	}
}

func TestInit_NoArgs(t *testing.T) {
	tr := New()
	_ = tr.Init()
	if !tr.Frozen() {
		t.Error("expected frozen after Init() with no args")
	}
}

func TestInit_InvalidFolder(t *testing.T) {
	tr := New()
	errs := tr.Init(
		WithFolder(filepath.Join(t.TempDir(), "nonexistent", "deep", "path")),
		WithFileExt(".yaml"),
	)
	if len(errs) != 0 {
		for _, e := range errs {
			if !os.IsNotExist(e) {
				t.Logf("non-fatal init error (expected for empty dir): %v", e)
			}
		}
	}
}

func TestInit_MalformedYAML(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("key: [invalid\n  broken:"), 0644); err != nil {
		t.Fatal(err)
	}
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	if len(errs) == 0 {
		t.Error("expected errors for malformed YAML")
	}
}

func TestInit_InvalidatesCache(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	_ = tr.T("GREETING")
	s1 := tr.Stats()
	if s1.CacheSize == 0 {
		t.Fatal("expected cache to be populated after T()")
	}

	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	s2 := tr.Stats()
	if s2.CacheSize != 0 {
		t.Errorf("cache not cleared after re-Init, size=%d", s2.CacheSize)
	}
}

// --- MustInit ---

func TestMustInit_Panics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic from MustInit")
		}
	}()
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("key: [invalid\n  broken:"), 0644)
	tr.MustInit(WithFolder(dir), WithFileExt(".yaml"))
}

func TestMustInit_NoPanic(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.MustInit(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
}

// --- Reload ---

func TestReload(t *testing.T) {
	tr, dir := newTestTranslator(t)
	opts := []Option{WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en")}
	tr.Init(opts...)

	msg := tr.T2("en", "GREETING")
	if msg != "Hello" {
		t.Fatalf("before reload: T2(en, GREETING) = %q, want Hello", msg)
	}

	writeYAML(t, dir, map[Anchor]Phrase{
		"GREETING":          "Hi there",
		"FAREWELL":          "Bye",
		"WITH_ARG":          "Hi, %s!",
		"AUTH.UNAUTHORIZED": "Not authorized",
		"REPO.NOT_FOUND":    "%s missing",
	})

	errs := tr.Reload(opts...)
	if len(errs) > 0 {
		t.Fatalf("Reload errors: %v", errs)
	}

	msg = tr.T2("en", "GREETING")
	if msg != "Hi there" {
		t.Errorf("after reload: T2(en, GREETING) = %q, want Hi there", msg)
	}
}

func TestReload_NoArgs(t *testing.T) {
	tr := New()
	_ = tr.Reload()
}

// --- T / T2 / TranslateAnchor ---

func TestT_ActiveLanguage(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.T("GREETING")
	if msg != "Привет" {
		t.Errorf("T(GREETING) = %q, want Привет", msg)
	}
}

func TestT2_ExplicitLanguage(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.T2("en", "GREETING")
	if msg != "Hello" {
		t.Errorf("T2(en, GREETING) = %q, want Hello", msg)
	}
}

func TestT_WithArgs(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	msg := tr.T("WITH_ARG", "World")
	if msg != "Hello, World!" {
		t.Errorf("T(WITH_ARG, World) = %q, want 'Hello, World!'", msg)
	}
}

func TestTranslateAnchor_FallbackToDefault(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.TranslateAnchor("fr", "GREETING")
	if msg != "Привет" {
		t.Errorf("TranslateAnchor(fr, GREETING) = %q, want Привет (fallback)", msg)
	}
}

func TestTranslateAnchor_MissingAnchor(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.TranslateAnchor("en", "NONEXISTENT")
	if msg != "NONEXISTENT" {
		t.Errorf("TranslateAnchor(en, NONEXISTENT) = %q, want anchor name", msg)
	}
}

func TestTranslateAnchor_CacheHit(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	first := tr.TranslateAnchor("en", "GREETING")
	second := tr.TranslateAnchor("en", "GREETING")
	if first != second {
		t.Errorf("cache inconsistency: %q != %q", first, second)
	}

	stats := tr.Stats()
	if stats.CacheSize == 0 {
		t.Error("expected cache to be populated")
	}
}

// --- TranslateError (errx bridge) ---

func TestTranslateError_ErrxError(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	xe := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "token expired")
	msg := tr.TranslateError("en", xe)
	if msg != "Unauthorized" {
		t.Errorf("TranslateError(en, AUTH.UNAUTHORIZED) = %q, want Unauthorized", msg)
	}

	msg = tr.TranslateError("ru", xe)
	if msg != "Не авторизован" {
		t.Errorf("TranslateError(ru, AUTH.UNAUTHORIZED) = %q, want 'Не авторизован'", msg)
	}
}

func TestTranslateError_ErrxWithArgs(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	xe := errx.New(errx.DomainRepo, errx.CodeNotFound, "user not found")
	msg := tr.TranslateError("en", xe)
	if msg != "%s not found" {
		t.Errorf("TranslateError = %q, want '%%s not found' (no args passed)", msg)
	}
}

func TestTranslateError_Fallback(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	xe := errx.New("CUSTOM", "CUSTOM_CODE", "custom message")
	msg := tr.TranslateError("en", xe)
	if msg != "custom message" {
		t.Errorf("TranslateError fallback = %q, want 'custom message'", msg)
	}
}

func TestTranslateError_PlainError(t *testing.T) {
	tr := New()
	msg := tr.TranslateError("en", errors.New("plain error"))
	if msg != "plain error" {
		t.Errorf("TranslateError(plain) = %q, want 'plain error'", msg)
	}
}

func TestTranslateError_WrappedErrx(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	xe := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "token expired")
	wrapped := fmt.Errorf("handler: %w", xe)
	msg := tr.TranslateError("en", wrapped)
	if msg != "Unauthorized" {
		t.Errorf("TranslateError(wrapped) = %q, want Unauthorized", msg)
	}
}

func TestTranslateError_Nil(t *testing.T) {
	tr := New()
	msg := tr.TranslateError("en", nil)
	if msg != "" {
		t.Errorf("TranslateError(nil) = %q, want empty", msg)
	}
}

// --- SetLanguage / Language ---

func TestSetLanguage(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	got := tr.SetLanguage("en")
	if got != "en" {
		t.Errorf("SetLanguage(en) = %q, want en", got)
	}
	if tr.Language() != "en" {
		t.Errorf("Language() = %q after SetLanguage(en)", tr.Language())
	}
}

func TestSetLanguage_Unavailable(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	got := tr.SetLanguage("fr")
	if got != "ru" {
		t.Errorf("SetLanguage(fr) = %q, want ru (unchanged)", got)
	}
}

// --- Languages ---

func TestLanguages(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	langs := tr.Languages()
	if len(langs) < 2 {
		t.Fatalf("Languages() = %v, want at least [ru, en]", langs)
	}

	has := func(l Locale) bool {
		for _, v := range langs {
			if v == l {
				return true
			}
		}
		return false
	}
	if !has("ru") || !has("en") {
		t.Errorf("Languages() = %v, missing ru or en", langs)
	}
}

// --- HasLanguage / HasTranslation ---

func TestHasLanguage(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	if !tr.HasLanguage("en") {
		t.Error("HasLanguage(en) = false, want true")
	}
	if tr.HasLanguage("fr") {
		t.Error("HasLanguage(fr) = true, want false")
	}
}

func TestHasTranslation(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	if !tr.HasTranslation("en", "GREETING") {
		t.Error("HasTranslation(en, GREETING) = false")
	}
	if tr.HasTranslation("en", "NONEXISTENT") {
		t.Error("HasTranslation(en, NONEXISTENT) = true")
	}
	if tr.HasTranslation("fr", "GREETING") {
		t.Error("HasTranslation(fr, GREETING) = true (no fr dict)")
	}
}

// --- Stats ---

func TestStats(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	stats := tr.Stats()
	if stats.Anchors != 5 {
		t.Errorf("Stats.Anchors = %d, want 5", stats.Anchors)
	}
	if stats.Languages < 2 {
		t.Errorf("Stats.Languages = %d, want >= 2", stats.Languages)
	}
	if stats.Translations < 10 {
		t.Errorf("Stats.Translations = %d, want >= 10", stats.Translations)
	}
}

// --- ClearCache ---

func TestClearCache(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	_ = tr.T("GREETING")
	if tr.Stats().CacheSize == 0 {
		t.Fatal("cache should be populated")
	}

	tr.ClearCache()
	if tr.Stats().CacheSize != 0 {
		t.Error("cache should be empty after ClearCache")
	}
}

// --- SaveDefault ---

func TestSaveDefault(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	if err := tr.SaveDefault("default"); err != nil {
		t.Fatalf("SaveDefault: %v", err)
	}

	path := filepath.Join(dir, "default.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if len(data) == 0 {
		t.Error("saved file is empty")
	}
}

// --- Frozen ---

func TestFrozen(t *testing.T) {
	tr := New()
	if tr.Frozen() {
		t.Error("new translator should not be frozen")
	}

	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))
	if !tr.Frozen() {
		t.Error("should be frozen after Init")
	}
}

// --- Folder / FileExt ---

func TestFolderAndFileExt(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yml"), WithLanguage("ru"))

	if tr.Folder() != dir {
		t.Errorf("Folder() = %q, want %q", tr.Folder(), dir)
	}
	if tr.FileExt() != ".yml" {
		t.Errorf("FileExt() = %q, want .yml", tr.FileExt())
	}
}

// --- Default / SetDefault (global wrappers) ---

func TestDefault_SetDefault(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr := New()
	tr.NewPhrase("CUSTOM", "custom value")
	SetDefault(tr)

	if Default() != tr {
		t.Error("Default() should return the translator set by SetDefault")
	}

	msg := T("CUSTOM")
	if msg != "custom value" {
		t.Errorf("global T(CUSTOM) = %q, want 'custom value'", msg)
	}
}

func TestGlobal_T(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	msg := T("GREETING")
	if msg != "Hello" {
		t.Errorf("global T(GREETING) = %q, want Hello", msg)
	}
}

func TestGlobal_T2(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	msg := T2("ru", "GREETING")
	if msg != "Привет" {
		t.Errorf("global T2(ru, GREETING) = %q, want Привет", msg)
	}
}

func TestGlobal_TranslateError(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	xe := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "expired")
	msg := TranslateError("en", xe)
	if msg != "Unauthorized" {
		t.Errorf("global TranslateError = %q, want Unauthorized", msg)
	}
}

func TestGlobal_TranslateAnchor(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	msg := TranslateAnchor("en", "FAREWELL")
	if msg != "Goodbye" {
		t.Errorf("global TranslateAnchor = %q, want Goodbye", msg)
	}
}

func TestGlobal_Init(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr := New()
	tr.NewPhrase("X", "x")
	SetDefault(tr)

	errs := Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))
	if len(errs) != 0 {
		t.Errorf("global Init errors: %v", errs)
	}
	if !Frozen() {
		t.Error("global Frozen() should be true after Init")
	}
}

func TestGlobal_MustInit(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr := New()
	SetDefault(tr)
	MustInit(WithFolder(t.TempDir()), WithFileExt(".yaml"))
}

func TestGlobal_Reload(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	errs := Reload(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	if len(errs) != 0 {
		t.Errorf("global Reload errors: %v", errs)
	}
}

func TestGlobal_NewPhrase(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr := New()
	SetDefault(tr)
	got := NewPhrase("GLOBAL_TEST", "global test")
	if got != "global test" {
		t.Errorf("global NewPhrase returned %q", got)
	}
}

func TestGlobal_SetLanguage(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	SetDefault(tr)

	SetLanguage("en")
	if Language() != "en" {
		t.Errorf("global Language() = %q after SetLanguage(en)", Language())
	}
}

func TestGlobal_Languages(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	SetDefault(tr)

	langs := Languages()
	if len(langs) < 2 {
		t.Errorf("global Languages() = %v, want >= 2", langs)
	}
}

func TestGlobal_QueryHelpers(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	SetDefault(tr)

	if Folder() != dir {
		t.Errorf("global Folder() = %q", Folder())
	}
	if FileExt() != ".yaml" {
		t.Errorf("global FileExt() = %q", FileExt())
	}
	if !HasLanguage("en") {
		t.Error("global HasLanguage(en) = false")
	}
	if !HasTranslation("en", "GREETING") {
		t.Error("global HasTranslation(en, GREETING) = false")
	}
	anchors := Anchors()
	if len(anchors) == 0 {
		t.Error("global Anchors() is empty")
	}
	stats := GetStats()
	if stats.Anchors == 0 {
		t.Error("global Stats().Anchors = 0")
	}
}

func TestGlobal_ClearCache(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	SetDefault(tr)

	_ = T("GREETING")
	ClearCache()
	if GetStats().CacheSize != 0 {
		t.Error("global ClearCache did not clear")
	}
}

func TestGlobal_SaveDefault(t *testing.T) {
	original := Default()
	defer SetDefault(original)

	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	SetDefault(tr)

	if err := SaveDefault("test_default"); err != nil {
		t.Fatalf("global SaveDefault: %v", err)
	}
}

// --- defaultConfig / Options ---

func TestDefaultConfig(t *testing.T) {
	c := defaultConfig()
	if c.folder != DefaultFolder {
		t.Errorf("folder = %q, want %q", c.folder, DefaultFolder)
	}
	if c.fileExt != DefaultFileExt {
		t.Errorf("fileExt = %q, want %q", c.fileExt, DefaultFileExt)
	}
	if c.language != DefaultLang {
		t.Errorf("language = %q, want %q", c.language, DefaultLang)
	}
	if c.devMode {
		t.Error("devMode should be false by default")
	}
}

func TestDefaultConfig_WithDevMode(t *testing.T) {
	c := defaultConfig()
	WithDevMode()(&c)
	if !c.devMode {
		t.Error("devMode should be true after WithDevMode()")
	}
}

// --- DevMode / detectDuplicates ---

func TestInit_DevMode(t *testing.T) {
	tr := New()
	tr.NewPhrase("SAME", "same text")
	tr.NewPhrase("ALSO_SAME", "same text")

	dir := t.TempDir()
	writeYAML(t, dir, map[Anchor]Phrase{
		"SAME":      "same text en",
		"ALSO_SAME": "same text en",
	})

	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"), WithDevMode())

	dupFile := filepath.Join(dir, "_duplicates.yaml")
	if _, err := os.Stat(dupFile); os.IsNotExist(err) {
		t.Error("_duplicates.yaml not created in DevMode")
	}
}

// --- Auto-sync (disk write) ---

func TestInit_AutoSync_DevMode(t *testing.T) {
	tr := New()
	tr.NewPhrase("A", "a")
	tr.NewPhrase("B", "b")

	dir := t.TempDir()
	writeYAML(t, dir, map[Anchor]Phrase{
		"A": "a-en",
	})

	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"), WithDevMode())

	data, err := os.ReadFile(filepath.Join(dir, "en.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "B:") {
		t.Error("auto-sync should have added missing anchor B to en.yaml in DevMode")
	}
}

func TestInit_NoAutoSync_WithoutDevMode(t *testing.T) {
	tr := New()
	tr.NewPhrase("A", "a")
	tr.NewPhrase("B", "b")

	dir := t.TempDir()
	writeYAML(t, dir, map[Anchor]Phrase{
		"A": "a-en",
	})

	srcData, _ := os.ReadFile(filepath.Join(dir, "en.yaml"))

	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	dstData, err := os.ReadFile(filepath.Join(dir, "en.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(srcData, dstData) {
		t.Error("Init without DevMode should not modify YAML files on disk")
	}
}

// --- formatString ---

func TestFormatString_ErrorArg(t *testing.T) {
	tr := New()
	tr.NewPhrase("ERR_TMPL", "error: %v happened")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.T("ERR_TMPL", errors.New("boom"))
	if msg != "error: boom happened" {
		t.Errorf("T(ERR_TMPL) = %q, want 'error: boom happened'", msg)
	}
}

// --- Edge cases for coverage ---

func TestSaveDefault_MkdirError(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))

	err := tr.SaveDefault(string([]byte{0}))
	if err == nil {
		t.Error("expected error from SaveDefault with invalid filename")
	}
}

func TestProcessDictionaries_ReadDirError(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	errs := tr.Init(
		WithFolder(filepath.Join(t.TempDir(), string([]byte{0}))),
		WithFileExt(".yaml"),
	)
	if len(errs) == 0 {
		t.Error("expected errors for invalid folder path")
	}
}

func TestProcessDictionaries_SkipsDirs(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "subdir.yaml"), 0755)
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestProcessDictionaries_SkipsUnderscore(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "_skip.yaml"), []byte(`X: skipped`), 0644)
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestProcessDictionaries_SkipsWrongExt(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "en.json"), []byte(`{"X":"x"}`), 0644)
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestProcessDictionaries_SkipsDefaultLang(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ru.yaml"), []byte(`"X": "overridden"`), 0644)
	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	msg := tr.T("X")
	if msg != "x" {
		t.Errorf("T(X) = %q, want 'x' (default lang file should be skipped)", msg)
	}
}

func TestDetectDuplicates_NoDuplicates(t *testing.T) {
	tr := New()
	tr.NewPhrase("A", "alpha")
	tr.NewPhrase("B", "beta")
	dir := t.TempDir()
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"), WithDevMode())

	dupFile := filepath.Join(dir, "_duplicates.yaml")
	if _, err := os.Stat(dupFile); !os.IsNotExist(err) {
		t.Error("_duplicates.yaml should not be created when no duplicates exist")
	}
}

func TestFormatString_NoArgs(t *testing.T) {
	tr := New()
	tr.NewPhrase("PLAIN", "no args here")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))

	msg := tr.T("PLAIN")
	if msg != "no args here" {
		t.Errorf("T(PLAIN) = %q, want 'no args here'", msg)
	}
}

func TestProcessDictionaries_UnreadableFile(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	dir := t.TempDir()

	path := filepath.Join(dir, "en.yaml")
	os.WriteFile(path, []byte(`"X": "hello"`), 0644)
	os.Chmod(path, 0000)
	defer os.Chmod(path, 0644)

	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"))
	_ = errs
}

func TestProcessDictionaries_MarshalAndWriteBack(t *testing.T) {
	tr := New()
	tr.NewPhrase("A", "alpha")
	tr.NewPhrase("B", "beta")
	dir := t.TempDir()

	writeYAML(t, dir, map[Anchor]Phrase{
		"A": "alpha-en",
		"B": "beta-en",
	})

	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}

	msg := tr.T2("en", "A")
	if msg != "alpha-en" {
		t.Errorf("T2(en, A) = %q, want alpha-en", msg)
	}
}

func TestProcessDictionaries_DevMode_NoChangeSkipsWrite(t *testing.T) {
	tr := New()
	tr.NewPhrase("A", "alpha")
	dir := t.TempDir()

	writeYAML(t, dir, map[Anchor]Phrase{
		"A": "alpha-en",
	})

	errs := tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"), WithDevMode())
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	if tr.T2("en", "A") != "alpha-en" {
		t.Error("expected alpha-en")
	}
}

func TestDetectDuplicates_MultiplePhrases(t *testing.T) {
	tr := New()
	tr.NewPhrase("MULTI", "first version")
	tr.NewPhrase("MULTI", "second version")
	tr.NewPhrase("MULTI", "third version")

	dir := t.TempDir()
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"), WithDevMode())

	dupFile := filepath.Join(dir, "_duplicates.yaml")
	data, err := os.ReadFile(dupFile)
	if err != nil {
		t.Fatalf("reading _duplicates.yaml: %v", err)
	}
	if len(data) == 0 {
		t.Error("_duplicates.yaml should not be empty")
	}
}

func TestFormatString_AnchorArg(t *testing.T) {
	tr := New()
	tr.NewPhrase("TMPL", "value is: %s")
	tr.NewPhrase("INNER", "inner value")
	dir := t.TempDir()
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("ru"))

	msg := tr.T("TMPL", Anchor("INNER"))
	if msg != "value is: inner value" {
		t.Errorf("T(TMPL, Anchor) = %q, want 'value is: inner value'", msg)
	}
}

func TestFormatString_ErrorVerbArg(t *testing.T) {
	tr := New()
	tr.NewPhrase("ERR_PLAIN", "error: %v")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))

	msg := tr.T("ERR_PLAIN", errors.New("oops"))
	if msg != "error: oops" {
		t.Errorf("T(ERR_PLAIN) = %q, want 'error: oops'", msg)
	}
}

func TestFormatString_MultipleArgs(t *testing.T) {
	tr := New()
	tr.NewPhrase("MULTI_ARG", "%s has %d items")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))

	msg := tr.T("MULTI_ARG", "cart", 5)
	if msg != "cart has 5 items" {
		t.Errorf("T(MULTI_ARG) = %q, want 'cart has 5 items'", msg)
	}
}

func TestSaveDefault_InvalidFolder(t *testing.T) {
	tr := New()
	tr.NewPhrase("X", "x")
	tr.Init(WithFolder(t.TempDir()), WithFileExt(".yaml"))

	tr.mu.Lock()
	tr.dir = string([]byte{0})
	tr.mu.Unlock()

	err := tr.SaveDefault("test")
	if err == nil {
		t.Error("expected error from SaveDefault with null-byte folder")
	}
}

// --- Concurrency ---

func TestConcurrency(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = tr.T("GREETING")
			_ = tr.T2("ru", "FAREWELL")
			_ = tr.TranslateAnchor("en", "WITH_ARG", "test")
			_ = tr.TranslateError("en", errx.New(errx.DomainAuth, errx.CodeUnauthorized, "x"))
			_ = tr.Stats()
			_ = tr.Language()
			_ = tr.Languages()
			_ = tr.HasLanguage("en")
			_ = tr.HasTranslation("en", "GREETING")
		}()
	}
	wg.Wait()
}

func TestConcurrency_ClearCacheWhileTranslating(t *testing.T) {
	tr, dir := newTestTranslator(t)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = tr.T("GREETING")
		}()
		go func() {
			defer wg.Done()
			tr.ClearCache()
		}()
	}
	wg.Wait()
}

func TestSetDefault_NilPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on SetDefault(nil)")
		}
	}()
	SetDefault(nil)
}
