package cfgx_test

import (
	"errors"
	"fmt"
	"strings"

	"github.com/aasyanov/urx/cfgx"
)

// appConfig is a typical service config that implements cfgx.Validator.
type appConfig struct {
	Port int    `json:"port" yaml:"port"`
	Host string `json:"host" yaml:"host"`
}

func (c *appConfig) Validate(fix bool) []error {
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
		}
		return []error{errors.New("port must be > 0")}
	}
	return nil
}

// ExampleLoad reads a config file into a struct via an injected reader.
func ExampleLoad() {
	cfg := appConfig{Port: 8080, Host: "localhost"}

	err := cfgx.Load("config.yaml", &cfg,
		cfgx.WithReader(func(string) ([]byte, error) {
			return []byte("port: 9090\n"), nil
		}),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%d@%s\n", cfg.Port, cfg.Host)
	// Output: 9090@localhost
}

// ExampleParse decodes an in-memory byte slice — the filesystem-free
// counterpart of Load, ideal for embedded defaults and tests.
func ExampleParse() {
	var cfg appConfig
	data := []byte(`{"port": 9090, "host": "db.local"}`)

	if err := cfgx.Parse(data, &cfg, cfgx.WithFormat(cfgx.FormatJSON)); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Printf("%d@%s\n", cfg.Port, cfg.Host)
	// Output: 9090@db.local
}

// ExampleParse_autoFix shows the Validator seam repairing an invalid value
// while still reporting that a fix was necessary.
func ExampleParse_autoFix() {
	cfg := appConfig{}
	data := []byte(`{"port": 0}`)

	err := cfgx.Parse(data, &cfg, cfgx.WithFormat(cfgx.FormatJSON), cfgx.WithAutoFix())
	fmt.Println("port after fix:", cfg.Port)
	fmt.Println("validation reported:", errors.Is(err, cfgx.ErrValidationFailed))
	// Output:
	// port after fix: 8080
	// validation reported: true
}

type nestedServer struct {
	Port int `json:"port" yaml:"port"`
}

func (s *nestedServer) Validate(fix bool) []error {
	if s.Port <= 0 {
		if fix {
			s.Port = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type nestedConfig struct {
	Server nestedServer `json:"server" yaml:"server"`
}

// ExampleValidate_slice walks Validators on slice elements with index paths.
func ExampleValidate_slice() {
	type list struct {
		Servers []nestedServer `json:"servers"`
	}
	cfg := list{Servers: []nestedServer{{Port: 0}}}
	err := cfgx.Parse([]byte(`{"servers":[{"port":0}]}`), &cfg, cfgx.WithFormat(cfgx.FormatJSON))
	fmt.Println("slice validation:", errors.Is(err, cfgx.ErrValidationFailed))
	fmt.Println("index path:", strings.Contains(err.Error(), "servers[0]"))
	// Output:
	// slice validation: true
	// index path: true
}

// ExampleValidate_nested walks nested Validators and prefixes field paths.
func ExampleValidate_nested() {
	cfg := nestedConfig{Server: nestedServer{Port: 0}}
	err := cfgx.Parse([]byte(`{"server":{"port":0}}`), &cfg, cfgx.WithFormat(cfgx.FormatJSON))
	fmt.Println("nested validation:", errors.Is(err, cfgx.ErrValidationFailed))
	fmt.Println("path prefix:", strings.Contains(err.Error(), "server"))
	// Output:
	// nested validation: true
	// path prefix: true
}

// ExampleMarshal encodes a config to bytes in an explicit format.
func ExampleMarshal() {
	cfg := appConfig{Port: 3000, Host: "localhost"}

	data, err := cfgx.Marshal(&cfg, cfgx.FormatJSON)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Print(string(data))
	// Output:
	// {
	//   "port": 3000,
	//   "host": "localhost"
	// }
}

// ExampleLoad_pipeline demonstrates the cfgx → envx → clix precedence chain
// using only pointer sharing: file is the base layer, environment overrides
// it, and (in a real program) clix flags would override last. Here we show
// the cfgx + envx-style overlay; clix would bind the same &cfg fields.
func ExampleLoad_pipeline() {
	// 1. Defaults.
	cfg := appConfig{Port: 8080, Host: "localhost"}

	// 2. File layer (injected reader stands in for disk).
	if err := cfgx.Load("config.yaml", &cfg,
		cfgx.WithReader(func(string) ([]byte, error) {
			return []byte("port: 9090\n"), nil // host omitted → keeps default
		}),
	); err != nil {
		fmt.Println("load:", err)
		return
	}

	// 3. Environment layer would call envx.BindTo(env, "HOST", &cfg.Host).
	//    Simulated here by writing the same pointer directly.
	cfg.Host = "db.prod" // e.g. from APP_HOST

	fmt.Printf("%d@%s\n", cfg.Port, cfg.Host)
	// Output: 9090@db.prod
}
