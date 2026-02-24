package dicx

import (
	"reflect"
)

// --- Lifetime ---

// Lifetime controls how many instances of a dependency are created.
type Lifetime uint8

const (
	// Singleton creates one instance and caches it for the container lifetime.
	Singleton Lifetime = iota
	// Transient creates a new instance on every [Resolve] call.
	Transient
)

// --- Provide options ---

// ProvideOption configures a provider during registration.
type ProvideOption func(*provideConfig)

// provideConfig holds provider registration parameters.
type provideConfig struct {
	lifetime Lifetime
}

// WithLifetime sets the [Lifetime] for the registered constructor.
// Defaults to [Singleton].
func WithLifetime(lt Lifetime) ProvideOption {
	return func(cfg *provideConfig) {
		cfg.lifetime = lt
	}
}

// --- Provider ---

// errorType caches the reflect.Type for the error interface.
var errorType = reflect.TypeFor[error]()

// provider holds a validated constructor with its dependency and lifetime metadata.
type provider struct {
	ctor     reflect.Value
	lifetime Lifetime
	inTypes  []reflect.Type
	outType  reflect.Type
	hasError bool
}

// newProvider validates a constructor and returns a provider or an error.
//
// Valid signatures:
//
//	func(...deps) T
//	func(...deps) (T, error)
func newProvider(constructor any, opts ...ProvideOption) (*provider, error) {
	cfg := provideConfig{lifetime: Singleton}
	for _, opt := range opts {
		opt(&cfg)
	}

	if constructor == nil {
		return nil, errBadConstructor("constructor must be a function")
	}

	v := reflect.ValueOf(constructor)
	t := v.Type()

	if t.Kind() != reflect.Func {
		return nil, errBadConstructor("constructor must be a function")
	}
	if t.IsVariadic() {
		return nil, errBadConstructor("variadic constructors are not supported")
	}

	numOut := t.NumOut()
	if numOut == 0 || numOut > 2 {
		return nil, errBadConstructor("constructor must return 1 or 2 values")
	}

	hasErr := false
	if numOut == 2 {
		if !t.Out(1).Implements(errorType) {
			return nil, errBadConstructor("second return value must implement error")
		}
		hasErr = true
	}

	inTypes := make([]reflect.Type, t.NumIn())
	for i := range inTypes {
		inTypes[i] = t.In(i)
	}

	return &provider{
		ctor:     v,
		lifetime: cfg.lifetime,
		inTypes:  inTypes,
		outType:  t.Out(0),
		hasError: hasErr,
	}, nil
}
