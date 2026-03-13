// Example: Complete service skeleton using URX components.
//
// Demonstrates: ctxx (tracing) → logx (structured logging) → healthx
// (health probes) → signalx (graceful shutdown) → errx (error model)
// together with resilience wrappers.
//
// Run:
//
//	go run ./examples/full-service
//
// Then open:
//
//	http://localhost:8080/api
//	http://localhost:8080/healthz
//	http://localhost:8080/readyz
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aasyanov/urx/pkg/bulkx"
	"github.com/aasyanov/urx/pkg/circuitx"
	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/healthx"
	"github.com/aasyanov/urx/pkg/logx"
	"github.com/aasyanov/urx/pkg/retryx"
	"github.com/aasyanov/urx/pkg/signalx"
)

func main() {
	logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil)))
	slog.SetDefault(logger)

	health := healthx.New(healthx.WithTimeout(2 * time.Second))
	health.Register("self", func(ctx context.Context) error { return nil })

	bh := bulkx.New(bulkx.WithMaxConcurrent(20))
	cb := circuitx.New(circuitx.WithMaxFailures(5), circuitx.WithResetTimeout(15*time.Second))

	mux := http.NewServeMux()
	mux.Handle("/healthz", health.LiveHandler())
	mux.Handle("/readyz", health.ReadyHandler())

	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		ctx := ctxx.WithTrace(r.Context())
		ctx = ctxx.WithSpan(ctx)
		ctx = logx.WithLogger(ctx, logger)

		traceID, spanID := ctxx.TraceFromContext(ctx)
		log := logx.FromContext(ctx)

		result, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (string, error) {
			return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (string, error) {
				return retryx.Do(ctx, func(rc retryx.RetryController) (string, error) {
					return fmt.Sprintf("OK from attempt %d (active=%d, circuit=%s)",
						rc.Number(), bc.Active(), cc.State()), nil
				}, retryx.WithMaxAttempts(3))
			})
		})

		if err != nil {
			log.Error("request failed", logx.Err(err))
			if ex, ok := errx.As(err); ok {
				http.Error(w, ex.Message, http.StatusServiceUnavailable)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("X-Trace-ID", traceID)
		w.Header().Set("X-Span-ID", spanID)
		log.Info("request handled", slog.String("result", result))
		fmt.Fprintln(w, result)
	})

	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		logger.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("server error", logx.Err(err))
		}
	}()

	if err := signalx.Wait(context.Background(), 10*time.Second, func(ctx context.Context) {
		logger.Info("shutting down...")
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("server shutdown error", logx.Err(err))
		}
	}); err != nil {
		logger.Error("shutdown error", logx.Err(err))
	}

	logger.Info("stopped")
}
