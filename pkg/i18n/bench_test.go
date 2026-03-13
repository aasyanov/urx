package i18n

import (
	"os"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

func setupBench(b *testing.B) *Translator {
	b.Helper()
	dir := b.TempDir()

	tr := New()
	tr.NewPhrase("GREETING", "Привет")
	tr.NewPhrase("FAREWELL", "До свидания")
	tr.NewPhrase("WITH_ARG", "Привет, %s!")
	tr.NewPhrase("AUTH.UNAUTHORIZED", "Не авторизован")
	tr.NewPhrase("REPO.NOT_FOUND", "%s не найден")

	en := `"GREETING": "Hello"
"FAREWELL": "Goodbye"
"WITH_ARG": "Hello, %s!"
"AUTH.UNAUTHORIZED": "Unauthorized"
"REPO.NOT_FOUND": "%s not found"
`
	os.WriteFile(dir+"/en.yaml", []byte(en), 0644)
	tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	return tr
}

// --- T (cached hot path) ---

func BenchmarkT_Cached(b *testing.B) {
	tr := setupBench(b)
	_ = tr.T("GREETING")
	b.ResetTimer()
	for b.Loop() {
		_ = tr.T("GREETING")
	}
}

// --- T (cold path, no cache) ---

func BenchmarkT_Uncached(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		tr.ClearCache()
		_ = tr.T("GREETING")
	}
}

// --- T2 with explicit language ---

func BenchmarkT2(b *testing.B) {
	tr := setupBench(b)
	_ = tr.T2("en", "GREETING")
	b.ResetTimer()
	for b.Loop() {
		_ = tr.T2("en", "GREETING")
	}
}

// --- T with formatting args ---

func BenchmarkT_WithArgs(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		_ = tr.T("WITH_ARG", "World")
	}
}

// --- TranslateAnchor direct ---

func BenchmarkTranslateAnchor_Cached(b *testing.B) {
	tr := setupBench(b)
	_ = tr.TranslateAnchor("en", "GREETING")
	b.ResetTimer()
	for b.Loop() {
		_ = tr.TranslateAnchor("en", "GREETING")
	}
}

func BenchmarkTranslateAnchor_Miss(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		_ = tr.TranslateAnchor("en", "NONEXISTENT")
	}
}

// --- TranslateError (errx bridge) ---

func BenchmarkTranslateError(b *testing.B) {
	tr := setupBench(b)
	xe := errx.New(errx.DomainAuth, errx.CodeUnauthorized, "token expired")
	_ = tr.TranslateError("en", xe)
	b.ResetTimer()
	for b.Loop() {
		_ = tr.TranslateError("en", xe)
	}
}

func BenchmarkTranslateError_Fallback(b *testing.B) {
	tr := setupBench(b)
	xe := errx.New("CUSTOM", "UNKNOWN", "custom message")
	b.ResetTimer()
	for b.Loop() {
		_ = tr.TranslateError("en", xe)
	}
}

func BenchmarkTranslateError_PlainError(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		_ = tr.TranslateError("en", os.ErrNotExist)
	}
}

// --- NewPhrase ---

func BenchmarkNewPhrase(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		tr := New()
		tr.NewPhrase("BENCH", "bench value")
	}
}

// --- Init ---

func BenchmarkInit(b *testing.B) {
	dir := b.TempDir()
	en := `"GREETING": "Hello"
"FAREWELL": "Goodbye"
`
	os.WriteFile(dir+"/en.yaml", []byte(en), 0644)

	b.ResetTimer()
	for b.Loop() {
		tr := New()
		tr.NewPhrase("GREETING", "Привет")
		tr.NewPhrase("FAREWELL", "До свидания")
		tr.Init(WithFolder(dir), WithFileExt(".yaml"), WithLanguage("en"))
	}
}

// --- SetLanguage ---

func BenchmarkSetLanguage(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		tr.SetLanguage("en")
	}
}

// --- Stats ---

func BenchmarkStats(b *testing.B) {
	tr := setupBench(b)
	b.ResetTimer()
	for b.Loop() {
		_ = tr.Stats()
	}
}

// --- Global T (through atomic.Pointer) ---

func BenchmarkGlobal_T(b *testing.B) {
	tr := setupBench(b)
	original := Default()
	SetDefault(tr)
	defer SetDefault(original)

	_ = T("GREETING")
	b.ResetTimer()
	for b.Loop() {
		_ = T("GREETING")
	}
}
