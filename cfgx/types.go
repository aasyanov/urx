package cfgx

// Format identifies a configuration file encoding.
type Format uint8

const (
	// FormatAuto detects the format from the file extension.
	FormatAuto Format = iota
	// FormatYAML selects YAML encoding (.yaml, .yml).
	FormatYAML
	// FormatJSON selects JSON encoding (.json).
	FormatJSON
	// FormatTOML selects TOML encoding (.toml).
	FormatTOML
)

// String returns the lowercase format name ("auto", "yaml", "json", "toml").
func (f Format) String() string {
	switch f {
	case FormatAuto:
		return "auto"
	case FormatYAML:
		return "yaml"
	case FormatJSON:
		return "json"
	case FormatTOML:
		return "toml"
	default:
		return "unknown"
	}
}

// Validator is an optional interface that config structs can implement.
// [Load] calls it automatically after unmarshalling: with fix=true when
// [WithAutoFix] is enabled, or fix=false (report only) otherwise.
//
// Implementations should collect all validation errors (not fail-fast on
// the first). When fix is true, the method may mutate the receiver to correct
// invalid values and should return only the errors that remain after the
// fix pass. This is the seam through which cfgx composes with envx (env
// overrides) and clix (flag overrides): validate the struct once after the
// whole precedence chain has been applied.
type Validator interface {
	Validate(fix bool) []error
}
