package cfgx

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- test config ---

type testCfg struct {
	Host    string `yaml:"host" json:"host" toml:"host"`
	Port    int    `yaml:"port" json:"port" toml:"port"`
	Debug   bool   `yaml:"debug" json:"debug" toml:"debug"`
	Timeout int    `yaml:"timeout" json:"timeout" toml:"timeout"`
}

// validatingCfg implements Validator.
type validatingCfg struct {
	Port int    `yaml:"port" json:"port" toml:"port"`
	Host string `yaml:"host" json:"host" toml:"host"`
}

func (c *validatingCfg) Validate(fix bool) []error {
	var errs []error
	if c.Port <= 0 || c.Port > 65535 {
		if fix {
			c.Port = 8080
		} else {
			errs = append(errs, fmt.Errorf("port %d out of range", c.Port))
		}
	}
	if c.Host == "" {
		if fix {
			c.Host = "0.0.0.0"
		} else {
			errs = append(errs, fmt.Errorf("host is empty"))
		}
	}
	return errs
}

func memReader(data []byte) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return data, nil }
}

func notFoundReader() func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return nil, os.ErrNotExist }
}

func failReader(msg string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return nil, fmt.Errorf("%s", msg) }
}

func captureWriter(out *[]byte) func(string, []byte, os.FileMode) error {
	return func(_ string, data []byte, _ os.FileMode) error {
		*out = data
		return nil
	}
}

func failWriter(msg string) func(string, []byte, os.FileMode) error {
	return func(string, []byte, os.FileMode) error { return fmt.Errorf("%s", msg) }
}

// --- YAML ---

func TestLoad_YAML(t *testing.T) {
	data := []byte("host: db.local\nport: 5432\ndebug: true\ntimeout: 30\n")
	var cfg testCfg
	err := Load("config.yaml", &cfg, WithReader(memReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "db.local" {
		t.Fatalf("host = %q, want db.local", cfg.Host)
	}
	if cfg.Port != 5432 {
		t.Fatalf("port = %d, want 5432", cfg.Port)
	}
	if !cfg.Debug {
		t.Fatal("debug should be true")
	}
	if cfg.Timeout != 30 {
		t.Fatalf("timeout = %d, want 30", cfg.Timeout)
	}
}

func TestLoad_YML(t *testing.T) {
	data := []byte("host: x\nport: 1\n")
	var cfg testCfg
	err := Load("app.yml", &cfg, WithReader(memReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "x" {
		t.Fatalf("host = %q, want x", cfg.Host)
	}
}

func TestSave_YAML(t *testing.T) {
	cfg := testCfg{Host: "localhost", Port: 8080}
	var out []byte
	err := Save("config.yaml", &cfg, WithWriter(captureWriter(&out)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}

	var loaded testCfg
	err = Load("config.yaml", &loaded, WithReader(memReader(out)))
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if loaded.Host != "localhost" || loaded.Port != 8080 {
		t.Fatalf("roundtrip failed: %+v", loaded)
	}
}

// --- JSON ---

func TestLoad_JSON(t *testing.T) {
	data := []byte(`{"host":"api.svc","port":3000,"debug":false,"timeout":10}`)
	var cfg testCfg
	err := Load("config.json", &cfg, WithReader(memReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "api.svc" {
		t.Fatalf("host = %q, want api.svc", cfg.Host)
	}
	if cfg.Port != 3000 {
		t.Fatalf("port = %d, want 3000", cfg.Port)
	}
}

func TestSave_JSON(t *testing.T) {
	cfg := testCfg{Host: "db", Port: 5432, Debug: true}
	var out []byte
	err := Save("config.json", &cfg, WithWriter(captureWriter(&out)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var loaded testCfg
	err = Load("config.json", &loaded, WithReader(memReader(out)))
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if loaded.Host != "db" || loaded.Port != 5432 || !loaded.Debug {
		t.Fatalf("roundtrip failed: %+v", loaded)
	}
}

// --- TOML ---

func TestLoad_TOML(t *testing.T) {
	data := []byte("host = \"cache\"\nport = 6379\ndebug = true\ntimeout = 5\n")
	var cfg testCfg
	err := Load("config.toml", &cfg, WithReader(memReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "cache" {
		t.Fatalf("host = %q, want cache", cfg.Host)
	}
	if cfg.Port != 6379 {
		t.Fatalf("port = %d, want 6379", cfg.Port)
	}
}

func TestSave_TOML(t *testing.T) {
	cfg := testCfg{Host: "redis", Port: 6379}
	var out []byte
	err := Save("config.toml", &cfg, WithWriter(captureWriter(&out)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var loaded testCfg
	err = Load("config.toml", &loaded, WithReader(memReader(out)))
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if loaded.Host != "redis" || loaded.Port != 6379 {
		t.Fatalf("roundtrip failed: %+v", loaded)
	}
}

// --- WithFormat override ---

func TestLoad_WithFormat(t *testing.T) {
	data := []byte(`{"host":"forced","port":1234}`)
	var cfg testCfg
	err := Load("config.txt", &cfg,
		WithReader(memReader(data)),
		WithFormat(FormatJSON),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Host != "forced" {
		t.Fatalf("host = %q, want forced", cfg.Host)
	}
}

// --- Unsupported format ---

func TestLoad_UnsupportedFormat(t *testing.T) {
	err := Load("config.ini", &testCfg{}, WithReader(memReader(nil)))
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeUnsupportedFormat {
		t.Fatalf("code = %q, want %q", xe.Code, CodeUnsupportedFormat)
	}
}

// --- Not found ---

func TestLoad_NotFound(t *testing.T) {
	err := Load("missing.yaml", &testCfg{}, WithReader(notFoundReader()))
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeNotFound {
		t.Fatalf("code = %q, want %q", xe.Code, CodeNotFound)
	}
}

// --- Read failure ---

func TestLoad_ReadFailed(t *testing.T) {
	err := Load("config.yaml", &testCfg{}, WithReader(failReader("disk error")))
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeReadFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeReadFailed)
	}
}

// --- Parse failure ---

func TestLoad_ParseFailed_YAML(t *testing.T) {
	err := Load("config.yaml", &testCfg{}, WithReader(memReader([]byte("{{{"))))
	if err == nil {
		t.Fatal("expected parse error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeParseFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeParseFailed)
	}
}

func TestLoad_ParseFailed_JSON(t *testing.T) {
	err := Load("config.json", &testCfg{}, WithReader(memReader([]byte("{bad"))))
	if err == nil {
		t.Fatal("expected parse error")
	}
	var xe *errx.Error
	errors.As(err, &xe)
	if xe.Code != CodeParseFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeParseFailed)
	}
}

func TestLoad_ParseFailed_TOML(t *testing.T) {
	err := Load("config.toml", &testCfg{}, WithReader(memReader([]byte("[[[bad"))))
	if err == nil {
		t.Fatal("expected parse error")
	}
	var xe *errx.Error
	errors.As(err, &xe)
	if xe.Code != CodeParseFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeParseFailed)
	}
}

// --- Write failure ---

func TestSave_WriteFailed(t *testing.T) {
	err := Save("config.yaml", &testCfg{}, WithWriter(failWriter("read-only fs")))
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeWriteFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeWriteFailed)
	}
}

// --- CreateIfMissing ---

func TestLoad_CreateIfMissing(t *testing.T) {
	cfg := testCfg{Host: "default", Port: 8080}
	var written []byte
	err := Load("config.yaml", &cfg,
		WithReader(notFoundReader()),
		WithWriter(captureWriter(&written)),
		WithCreateIfMissing(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("expected file to be written")
	}

	var reloaded testCfg
	err = Load("config.yaml", &reloaded, WithReader(memReader(written)))
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if reloaded.Host != "default" || reloaded.Port != 8080 {
		t.Fatalf("roundtrip failed: %+v", reloaded)
	}
}

func TestLoad_CreateIfMissing_JSON(t *testing.T) {
	cfg := testCfg{Host: "init", Port: 3000}
	var written []byte
	err := Load("config.json", &cfg,
		WithReader(notFoundReader()),
		WithWriter(captureWriter(&written)),
		WithCreateIfMissing(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var reloaded testCfg
	Load("config.json", &reloaded, WithReader(memReader(written)))
	if reloaded.Host != "init" {
		t.Fatalf("host = %q, want init", reloaded.Host)
	}
}

// --- Validator ---

func TestLoad_Validator_NoFix(t *testing.T) {
	data := []byte("port: -1\n")
	var cfg validatingCfg
	err := Load("config.yaml", &cfg, WithReader(memReader(data)))
	if err == nil {
		t.Fatal("expected validation error without autofix")
	}
	var me *errx.MultiError
	if !errors.As(err, &me) || me.Len() == 0 {
		t.Fatalf("expected *errx.MultiError, got %T: %v", err, err)
	}
	if cfg.Port != -1 {
		t.Fatalf("port should remain -1 without autofix, got %d", cfg.Port)
	}
}

func TestLoad_Validator_AutoFix(t *testing.T) {
	data := []byte("port: -1\n")
	var cfg validatingCfg
	err := Load("config.yaml", &cfg,
		WithReader(memReader(data)),
		WithAutoFix(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port should be fixed to 8080, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("host should be fixed to 0.0.0.0, got %q", cfg.Host)
	}
}

func TestLoad_Validator_AutoFix_Roundtrip(t *testing.T) {
	data := []byte(`{"port": 0}`)
	var cfg validatingCfg
	Load("cfg.json", &cfg,
		WithReader(memReader(data)),
		WithAutoFix(),
	)

	var saved []byte
	Save("cfg.json", &cfg, WithWriter(captureWriter(&saved)))

	var reloaded validatingCfg
	Load("cfg.json", &reloaded, WithReader(memReader(saved)))
	if reloaded.Port != 8080 || reloaded.Host != "0.0.0.0" {
		t.Fatalf("roundtrip after fix failed: %+v", reloaded)
	}
}

func TestLoad_NilDst(t *testing.T) {
	err := Load("config.yaml", nil, WithReader(memReader([]byte("host: x\n"))))
	if err == nil {
		t.Fatal("expected error for nil dst")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidInput {
		t.Fatalf("code = %q, want %q", xe.Code, CodeInvalidInput)
	}
}

func TestLoad_NonPointerDst(t *testing.T) {
	err := Load("config.yaml", testCfg{}, WithReader(memReader([]byte("host: x\n"))))
	if err == nil {
		t.Fatal("expected error for non-pointer dst")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidInput {
		t.Fatalf("code = %q, want %q", xe.Code, CodeInvalidInput)
	}
}

func TestSave_NilSrc(t *testing.T) {
	err := Save("config.yaml", nil)
	if err == nil {
		t.Fatal("expected error for nil src")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidInput {
		t.Fatalf("code = %q, want %q", xe.Code, CodeInvalidInput)
	}
}

// --- WithReader/WithWriter nil guard ---

func TestWithReader_Nil(t *testing.T) {
	data := []byte("host: ok\n")
	var cfg testCfg
	err := Load("config.yaml", &cfg,
		WithReader(memReader(data)),
		WithReader(nil),
	)
	if err != nil {
		t.Fatalf("nil reader should be ignored: %v", err)
	}
}

func TestWithWriter_Nil(t *testing.T) {
	var out []byte
	err := Save("config.yaml", &testCfg{Host: "x"},
		WithWriter(captureWriter(&out)),
		WithWriter(nil),
	)
	if err != nil {
		t.Fatalf("nil writer should be ignored: %v", err)
	}
}

// --- Error domains ---

func TestError_Domain(t *testing.T) {
	err := Load("missing.yaml", &testCfg{}, WithReader(notFoundReader()))
	var xe *errx.Error
	errors.As(err, &xe)
	if xe.Domain != DomainConfig {
		t.Fatalf("domain = %q, want %q", xe.Domain, DomainConfig)
	}
}

// --- Save: non-pointer src ---

func TestSave_NonPointerSrc(t *testing.T) {
	cfg := testCfg{Host: "val", Port: 3000}
	var out []byte
	err := Save("config.yaml", cfg, WithWriter(captureWriter(&out)))
	if err != nil {
		t.Fatalf("Save with non-pointer src should work: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
}

// --- Save: nil pointer ---

func TestSave_NilPointerSrc(t *testing.T) {
	var p *testCfg
	err := Save("config.yaml", p)
	if err == nil {
		t.Fatal("expected error for nil pointer src")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeInvalidInput {
		t.Fatalf("code = %q, want %q", xe.Code, CodeInvalidInput)
	}
}

// --- Save: unsupported format ---

func TestSave_UnsupportedFormat(t *testing.T) {
	err := Save("config.ini", &testCfg{Host: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeUnsupportedFormat {
		t.Fatalf("code = %q, want %q", xe.Code, CodeUnsupportedFormat)
	}
}

// --- CreateIfMissing + write failure ---

func TestLoad_CreateIfMissing_WriteFailed(t *testing.T) {
	err := Load("config.yaml", &testCfg{Host: "x"},
		WithReader(notFoundReader()),
		WithWriter(failWriter("read-only fs")),
		WithCreateIfMissing(),
	)
	if err == nil {
		t.Fatal("expected write error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeWriteFailed {
		t.Fatalf("code = %q, want %q", xe.Code, CodeWriteFailed)
	}
}

// --- CreateIfMissing + Validator ---

func TestLoad_CreateIfMissing_ValidatorAutoFix(t *testing.T) {
	cfg := validatingCfg{Port: -1, Host: ""}
	var written []byte
	err := Load("config.yaml", &cfg,
		WithReader(notFoundReader()),
		WithWriter(captureWriter(&written)),
		WithCreateIfMissing(),
		WithAutoFix(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port should be fixed to 8080, got %d", cfg.Port)
	}
	if cfg.Host != "0.0.0.0" {
		t.Fatalf("host should be fixed to 0.0.0.0, got %q", cfg.Host)
	}
	if len(written) == 0 {
		t.Fatal("expected file to be written")
	}

	var reloaded validatingCfg
	err = Load("config.yaml", &reloaded, WithReader(memReader(written)))
	if err != nil {
		t.Fatalf("reload error: %v", err)
	}
	if reloaded.Port != 8080 || reloaded.Host != "0.0.0.0" {
		t.Fatalf("written file should contain fixed values: %+v", reloaded)
	}
}

func TestLoad_CreateIfMissing_ValidatorNoFix(t *testing.T) {
	cfg := validatingCfg{Port: -1, Host: ""}
	err := Load("config.yaml", &cfg,
		WithReader(notFoundReader()),
		WithWriter(captureWriter(new([]byte))),
		WithCreateIfMissing(),
	)
	if err == nil {
		t.Fatal("expected validation error without autofix")
	}
	var me *errx.MultiError
	if !errors.As(err, &me) {
		t.Fatalf("expected *errx.MultiError, got %T: %v", err, err)
	}
}

// --- Default values preserved when file missing with create ---

func TestLoad_CreatePreservesDefaults(t *testing.T) {
	cfg := testCfg{Host: "prod.db", Port: 5432, Debug: false, Timeout: 60}
	var written []byte
	Load("config.toml", &cfg,
		WithReader(notFoundReader()),
		WithWriter(captureWriter(&written)),
		WithCreateIfMissing(),
	)

	var reloaded testCfg
	Load("config.toml", &reloaded, WithReader(memReader(written)))
	if reloaded.Host != "prod.db" || reloaded.Port != 5432 || reloaded.Timeout != 60 {
		t.Fatalf("defaults not preserved: %+v", reloaded)
	}
}
