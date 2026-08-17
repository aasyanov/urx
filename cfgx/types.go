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

const (
	formatNameAuto = "auto"
	formatNameYAML = "yaml"
	formatNameJSON = "json"
	formatNameTOML = "toml"
)

// String returns the lowercase format name ("auto", "yaml", "json", "toml").
func (f Format) String() string {
	switch f {
	case FormatAuto:
		return formatNameAuto
	case FormatYAML:
		return formatNameYAML
	case FormatJSON:
		return formatNameJSON
	case FormatTOML:
		return formatNameTOML
	default:
		return "unknown"
	}
}

// Validator is an optional interface that config structs can implement.
// [Load]/[Parse] walk exported nested structs, then slice/array elements
// and map values that can hold a [Validator], and call every [Validator]
// post-order (children, then parent) after unmarshalling: fix=true when
// [WithAutoFix] is enabled, otherwise report-only. Field-path prefixes are
// added to remaining errors (servers[0].port, limits[api].rate). Re-run
// the same walk after env/flags with [Validate].
//
// Leaf types should implement Validate; a composition root should only
// check cross-field invariants — cfgx already walks children, so a root
// that re-calls child.Validate will double-invoke.
//
// Implementations should collect all validation errors (not fail-fast on
// the first). When fix is true, the method may mutate the receiver to correct
// invalid values and should return only the errors that remain after the
// fix pass.
type Validator interface {
	Validate(fix bool) []error
}
