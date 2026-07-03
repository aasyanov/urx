package cfgx

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// jsonIndent is the indentation applied when encoding JSON config files.
const jsonIndent = "  "

// resolveFormat returns f unchanged when it is explicit, otherwise detects
// the format from path's extension. Returns [ErrUnsupportedFormat] when the
// extension is not recognised.
func resolveFormat(f Format, path string) (Format, error) {
	if f != FormatAuto {
		return f, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		return FormatYAML, nil
	case ".json":
		return FormatJSON, nil
	case ".toml":
		return FormatTOML, nil
	default:
		return FormatAuto, errUnsupportedFormat(path, ext)
	}
}

// unmarshal decodes data into dst using the codec for format. format must be
// resolved (not [FormatAuto]); an unresolved format is a programming error
// and panics, because resolveFormat is always called first.
func unmarshal(data []byte, dst any, format Format) error {
	switch format {
	case FormatYAML:
		return yaml.Unmarshal(data, dst)
	case FormatJSON:
		return json.Unmarshal(data, dst)
	case FormatTOML:
		return toml.Unmarshal(data, dst)
	default:
		panic(fmt.Sprintf("cfgx: unmarshal called with unresolved format %d", format))
	}
}

// marshal encodes src using the codec for format. format must be resolved
// (not [FormatAuto]); an unresolved format panics for the same reason as
// [unmarshal].
func marshal(src any, format Format) ([]byte, error) {
	switch format {
	case FormatYAML:
		return yaml.Marshal(src)
	case FormatJSON:
		data, err := json.MarshalIndent(src, "", jsonIndent)
		if err != nil {
			return nil, err
		}
		return append(data, '\n'), nil
	case FormatTOML:
		var buf bytes.Buffer
		enc := toml.NewEncoder(&buf)
		if err := enc.Encode(src); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	default:
		panic(fmt.Sprintf("cfgx: marshal called with unresolved format %d", format))
	}
}
