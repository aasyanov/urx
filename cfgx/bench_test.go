package cfgx

import "testing"

func BenchmarkParse_YAML(b *testing.B) {
	data := []byte("port: 9090\nhost: db.local\n")
	b.ResetTimer()
	for b.Loop() {
		var cfg testConfig
		_ = Parse(data, &cfg, WithFormat(FormatYAML))
	}
}

func BenchmarkParse_JSON(b *testing.B) {
	data := []byte(`{"port":9090,"host":"db.local"}`)
	b.ResetTimer()
	for b.Loop() {
		var cfg testConfig
		_ = Parse(data, &cfg, WithFormat(FormatJSON))
	}
}

func BenchmarkParse_TOML(b *testing.B) {
	data := []byte("port = 9090\nhost = \"db.local\"\n")
	b.ResetTimer()
	for b.Loop() {
		var cfg testConfig
		_ = Parse(data, &cfg, WithFormat(FormatTOML))
	}
}

func BenchmarkMarshal_YAML(b *testing.B) {
	cfg := testConfig{Port: 9090, Host: "db.local"}
	b.ResetTimer()
	for b.Loop() {
		_, _ = Marshal(&cfg, FormatYAML)
	}
}

func BenchmarkMarshal_JSON(b *testing.B) {
	cfg := testConfig{Port: 9090, Host: "db.local"}
	b.ResetTimer()
	for b.Loop() {
		_, _ = Marshal(&cfg, FormatJSON)
	}
}

func BenchmarkLoad_InjectedReader(b *testing.B) {
	reader := staticReader([]byte("port: 9090\nhost: db.local\n"), nil)
	b.ResetTimer()
	for b.Loop() {
		var cfg testConfig
		_ = Load("config.yaml", &cfg, WithReader(reader))
	}
}

func BenchmarkParse_JSON_Parallel(b *testing.B) {
	data := []byte(`{"port":9090,"host":"db.local"}`)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var cfg testConfig
			_ = Parse(data, &cfg, WithFormat(FormatJSON))
		}
	})
}

func BenchmarkLoad_InjectedReader_Parallel(b *testing.B) {
	reader := staticReader([]byte("port: 9090\nhost: db.local\n"), nil)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var cfg testConfig
			_ = Load("config.yaml", &cfg, WithReader(reader))
		}
	})
}

func BenchmarkResolveFormat(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		_, _ = resolveFormat(FormatAuto, "config.yaml")
	}
}
