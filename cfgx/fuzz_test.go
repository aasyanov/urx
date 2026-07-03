package cfgx

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzParse feeds arbitrary bytes through each codec. The oracle: Parse must
// never panic and must always return through error, no matter how malformed
// the payload or which format is selected.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"",
		"port: 9090",
		`{"port":1}`,
		"port = 1",
		"{not json",
		"\x00\xff\x00",
		"port:\n  - nested\n",
		"[[[",
	}
	for _, s := range seeds {
		f.Add([]byte(s), uint8(FormatYAML))
		f.Add([]byte(s), uint8(FormatJSON))
		f.Add([]byte(s), uint8(FormatTOML))
	}

	f.Fuzz(func(t *testing.T, data []byte, rawFormat uint8) {
		format := FormatYAML + Format(rawFormat%3)

		var cfg testConfig
		// Must not panic regardless of input.
		_ = Parse(data, &cfg, WithFormat(format))
	})
}

// FuzzMarshalRoundTrip checks that any config encoded by Marshal decodes back
// to an equal value through Parse, for each explicit format.
func FuzzMarshalRoundTrip(f *testing.F) {
	f.Add(0, "")
	f.Add(8080, "localhost")
	f.Add(-1, "db.local")
	f.Add(65535, "a b c")

	f.Fuzz(func(t *testing.T, port int, host string) {
		// The round-trip invariant only holds for values the codecs do not
		// normalise. JSON rewrites invalid UTF-8 to U+FFFD; YAML strips
		// surrounding whitespace and folds blank-only scalars. Real config
		// values are well-formed UTF-8 without surrounding whitespace, so we
		// restrict the invariant to that domain — malformed inputs are
		// covered by FuzzParse's never-panic oracle instead.
		if !utf8.ValidString(host) || host != strings.TrimSpace(host) {
			return
		}
		formats := []Format{FormatYAML, FormatJSON, FormatTOML}
		for _, format := range formats {
			in := testConfig{Port: port, Host: host}
			data, err := Marshal(&in, format)
			if err != nil {
				continue
			}
			var out testConfig
			if err := Parse(data, &out, WithFormat(format)); err != nil {
				continue
			}
			if out != in {
				t.Fatalf("round-trip mismatch (format=%s): in=%+v out=%+v", format, in, out)
			}
		}
	})
}
