// Example: Configuration loading and dependency injection using URX.
//
// Demonstrates the full service bootstrap pattern: cfgx (file config) →
// envx (env overrides) → validx (validation) → dicx (dependency injection)
// → healthx (probes) → signalx (shutdown).
//
// Run:
//
//	APP_PORT=9090 APP_DEBUG=true go run ./examples/config-di
//
// The example creates a config.yaml if missing, loads it, applies env
// overrides, validates, wires dependencies via dicx, and starts a server.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aasyanov/urx/pkg/cfgx"
	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/dicx"
	"github.com/aasyanov/urx/pkg/envx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/healthx"
	"github.com/aasyanov/urx/pkg/logx"
	"github.com/aasyanov/urx/pkg/signalx"
	"github.com/aasyanov/urx/pkg/validx"
)

type Config struct {
	Port    int    `yaml:"port"    json:"port"`
	Host    string `yaml:"host"    json:"host"`
	Debug   bool   `yaml:"debug"   json:"debug"`
	AppName string `yaml:"appName" json:"appName"`
}

func (c *Config) Validate(fix bool) error {
	return validx.Collect(
		validx.Between("port", c.Port, 1, 65535),
		validx.Required("host", c.Host),
		validx.Required("appName", c.AppName),
	)
}

type Database struct {
	dsn string
}

func NewDatabase() *Database { return &Database{dsn: "postgres://localhost:5432/app"} }
func (d *Database) Start(_ context.Context) error {
	slog.Info("database connected", slog.String("dsn", d.dsn))
	return nil
}
func (d *Database) Stop(_ context.Context) error {
	slog.Info("database disconnected")
	return nil
}
func (d *Database) Ping(_ context.Context) error { return nil }

type UserService struct {
	db *Database
}

func NewUserService(db *Database) *UserService { return &UserService{db: db} }

func main() {
	logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil)))
	slog.SetDefault(logger)

	cfg := Config{
		Port:    8080,
		Host:    "0.0.0.0",
		Debug:   false,
		AppName: "urx-demo",
	}

	if err := cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix(), cfgx.WithCreateIfMissing()); err != nil {
		if ex, ok := errx.As(err); ok && ex.Code != "NOT_FOUND" {
			logger.Error("config load failed", logx.Err(err))
			os.Exit(1)
		}
	}

	env := envx.New(envx.WithPrefix("APP"))
	envx.BindTo(env, "PORT", &cfg.Port)
	envx.BindTo(env, "HOST", &cfg.Host)
	envx.BindTo(env, "DEBUG", &cfg.Debug)
	if err := env.Validate(); err != nil {
		logger.Error("env validation failed", logx.Err(err))
		os.Exit(1)
	}

	logger.Info("config loaded",
		slog.Int("port", cfg.Port),
		slog.String("host", cfg.Host),
		slog.Bool("debug", cfg.Debug),
		slog.String("app", cfg.AppName),
	)

	c := dicx.New()
	c.Provide(NewDatabase)
	c.Provide(NewUserService)

	ctx := ctxx.WithTrace(context.Background())
	if err := c.Start(ctx); err != nil {
		logger.Error("DI start failed", logx.Err(err))
		os.Exit(1)
	}
	defer c.Stop(ctx)

	db := dicx.MustResolve[*Database](c)
	_ = dicx.MustResolve[*UserService](c)

	health := healthx.New(healthx.WithTimeout(3 * time.Second))
	health.Register("database", db.Ping)

	mux := http.NewServeMux()
	mux.Handle("/healthz", health.LiveHandler())
	mux.Handle("/readyz", health.ReadyHandler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		report := health.Readiness(r.Context())
		fmt.Fprintf(w, "%s is %s\n", cfg.AppName, report.Status)
	})

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		logger.Info("listening", slog.String("addr", addr))
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("server error", logx.Err(err))
		}
	}()

	signalx.Wait(ctx, 10*time.Second, func(ctx context.Context) {
		srv.Shutdown(ctx)
		logger.Info("shutdown complete")
	})
}
