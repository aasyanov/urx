package cfgx

import (
	"os"
	"testing"
)

var yamlData = []byte("host: bench.local\nport: 9090\ndebug: true\ntimeout: 30\n")
var jsonData = []byte(`{"host":"bench.local","port":9090,"debug":true,"timeout":30}`)
var tomlData = []byte("host = \"bench.local\"\nport = 9090\ndebug = true\ntimeout = 30\n")

func BenchmarkLoad_YAML(b *testing.B) {
	r := func(string) ([]byte, error) { return yamlData, nil }
	b.ReportAllocs()
	for b.Loop() {
		var cfg testCfg
		Load("c.yaml", &cfg, WithReader(r))
	}
}

func BenchmarkLoad_JSON(b *testing.B) {
	r := func(string) ([]byte, error) { return jsonData, nil }
	b.ReportAllocs()
	for b.Loop() {
		var cfg testCfg
		Load("c.json", &cfg, WithReader(r))
	}
}

func BenchmarkLoad_TOML(b *testing.B) {
	r := func(string) ([]byte, error) { return tomlData, nil }
	b.ReportAllocs()
	for b.Loop() {
		var cfg testCfg
		Load("c.toml", &cfg, WithReader(r))
	}
}

func BenchmarkSave_YAML(b *testing.B) {
	cfg := testCfg{Host: "bench", Port: 8080, Debug: true, Timeout: 30}
	w := func(string, []byte, os.FileMode) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		Save("c.yaml", &cfg, WithWriter(w))
	}
}

func BenchmarkSave_JSON(b *testing.B) {
	cfg := testCfg{Host: "bench", Port: 8080, Debug: true, Timeout: 30}
	w := func(string, []byte, os.FileMode) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		Save("c.json", &cfg, WithWriter(w))
	}
}

func BenchmarkSave_TOML(b *testing.B) {
	cfg := testCfg{Host: "bench", Port: 8080, Debug: true, Timeout: 30}
	w := func(string, []byte, os.FileMode) error { return nil }
	b.ReportAllocs()
	for b.Loop() {
		Save("c.toml", &cfg, WithWriter(w))
	}
}
