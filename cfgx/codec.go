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

// yamlMarshal encodes src via gopkg.in/yaml.v3, converting encoder panics
// (unsupported types such as func()) into errors so [Save] never crashes.
func yamlMarshal(src any) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("yaml: %v", r)
		}
	}()
	return yaml.Marshal(src)
}

// ensureTrailingNewline appends a POSIX newline when absent so every on-disk
// config format ends consistently regardless of codec defaults.
func ensureTrailingNewline(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] != '\n' {
		return append(data, '\n')
	}
	return data
}

// marshal encodes src using the codec for format. format must be resolved
// (not [FormatAuto]); an unresolved format panics for the same reason as
// [unmarshal]. Every successful encode ends with a trailing newline.
func marshal(src any, format Format) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	switch format {
	case FormatYAML:
		data, err = yamlMarshal(src)
	case FormatJSON:
		data, err = json.MarshalIndent(src, "", jsonIndent)
	case FormatTOML:
		var buf bytes.Buffer
		enc := toml.NewEncoder(&buf)
		if encErr := enc.Encode(src); encErr != nil {
			return nil, encErr
		}
		data = buf.Bytes()
	default:
		panic(fmt.Sprintf("cfgx: marshal called with unresolved format %d", format))
	}
	if err != nil {
		return nil, err
	}
	return ensureTrailingNewline(data), nil
}
