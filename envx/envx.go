// Package envx provides typed environment-variable reading with injectable
// lookup for production Go services.
//
// An [Env] reads variables through a configurable lookup function (defaults
// to [os.LookupEnv]) and converts them to typed values via generic [Bind].
// All unresolved required variables and parse failures are collected into a
// single joined error on [Env.Validate].
//
//	env := envx.New(envx.WithPrefix("APP"))
//
//	port := envx.Bind(env, "PORT", 8080)
//	host := envx.Bind(env, "DB_HOST", "localhost")
//	secret := envx.BindRequired[string](env, "SECRET")
//
//	if err := env.Validate(); err != nil {
//	    log.Fatal(err) // APP_SECRET is required
//	}
//
//	fmt.Println(port.Value())   // 8080 or from APP_PORT
//	fmt.Println(secret.Value()) // from APP_SECRET
//
// # Supported types
//
// Bind supports string, bool, int, int32, int64, uint, float64,
// [time.Duration], and []string (comma-separated, whitespace-trimmed).
//
// # Overlaying onto a config struct (cfgx → envx → clix)
//
// envx is the environment layer of the precedence pipeline. Use [BindTo] to
// overlay variables onto a struct already populated by cfgx, then let clix
// flags override on top — all through plain pointer sharing:
//
//	cfgx.Load("config.yaml", &cfg)        // file layer
//	envx.BindTo(env, "PORT", &cfg.Port)   // env overrides file
//	clix.AddFlag(&cfg.Port, "port", ...)  // flag overrides env
//
// envx imports no other urx subpackage; the layers compose via pointers.
//
// # Testing
//
// Inject a custom lookup to avoid touching the real environment:
//
//	env := envx.New(
//	    envx.WithPrefix("APP"),
//	    envx.WithLookup(envx.MapLookup(map[string]string{
//	        "APP_PORT":   "9090",
//	        "APP_SECRET": "test-key",
//	    })),
//	)
//
// # Zero dependencies
//
// envx depends only on the Go standard library.
package envx

import "errors"

// New creates an [Env] with the given options. With no options it reads from
// the real process environment via [os.LookupEnv] and applies no prefix.
func New(opts ...Option) *Env {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Env{cfg: cfg}
}

// Validate checks every bound variable: required variables must be present
// and all present values must have parsed successfully. Returns a single
// joined error describing all failures, or nil when every binding is valid.
//
// Errors wrap [ErrMissing] (required but absent) or [ErrInvalid] (present
// but unparseable); use [errors.Is] to distinguish them.
func (e *Env) Validate() error {
	var errs []error
	for _, v := range e.vars {
		if err := v.validate(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Vars returns the full names (prefix applied) of all bound variables, in
// bind order. Useful for logging the effective configuration surface.
func (e *Env) Vars() []string {
	names := make([]string, len(e.vars))
	for i, v := range e.vars {
		names[i] = v.name()
	}
	return names
}
