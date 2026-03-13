// Example: HTTP rate-limiting middleware using URX.
//
// Demonstrates per-user rate limiting (quotax), global rate limiting
// (ratex), password hashing (hashx), and structured error responses
// (errx) — a typical API gateway pattern.
//
// Run:
//
//	go run ./examples/rate-middleware
//
// Then test:
//
//	curl -H "X-User: alice" http://localhost:8082/api
//	for i in $(seq 1 15); do curl -s -H "X-User: alice" http://localhost:8082/api; done
//	curl http://localhost:8082/hash -d "password=secret123"
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/hashx"
	"github.com/aasyanov/urx/pkg/logx"
	"github.com/aasyanov/urx/pkg/quotax"
	"github.com/aasyanov/urx/pkg/ratex"
	"github.com/aasyanov/urx/pkg/signalx"
)

func main() {
	logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	slog.SetDefault(logger)

	globalRL := ratex.New(ratex.WithRate(50), ratex.WithBurst(10))

	perUserRL := quotax.New(
		quotax.WithRate(5),
		quotax.WithBurst(3),
		quotax.WithMaxKeys(10000),
		quotax.WithEvictionTTL(10*time.Minute),
	)
	defer perUserRL.Close()

	hasher := hashx.New(hashx.WithAlgorithm(hashx.Argon2id), hashx.WithTier(hashx.TierDefault))

	rateLimitMiddleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ctxx.WithTrace(r.Context())
			ctx = logx.WithLogger(ctx, logger)

			if !globalRL.Allow() {
				writeError(w, http.StatusServiceUnavailable,
					errx.New("GATEWAY", "GLOBAL_LIMIT", "server is overloaded"))
				return
			}

			userID := r.Header.Get("X-User")
			if userID == "" {
				userID = r.RemoteAddr
			}

			if err := perUserRL.AllowOrError(userID); err != nil {
				logx.FromContext(ctx).Warn("rate limited",
					slog.String("user", userID),
					logx.Err(err),
				)
				writeError(w, http.StatusTooManyRequests, err)
				return
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		log := logx.FromContext(r.Context())
		traceID, _ := ctxx.TraceFromContext(r.Context())

		log.Info("request handled", slog.String("user", r.Header.Get("X-User")))

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Trace-ID", traceID)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":   "ok",
			"trace_id": traceID,
			"user":     r.Header.Get("X-User"),
		})
	})

	mux.HandleFunc("/hash", func(w http.ResponseWriter, r *http.Request) {
		password := r.FormValue("password")
		if password == "" {
			writeError(w, http.StatusBadRequest,
				errx.New("AUTH", "EMPTY_PASSWORD", "password is required"))
			return
		}

		hash, err := hasher.Generate(r.Context(), password)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		if err := hasher.Compare(r.Context(), hash, password); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"hash":     hash,
			"verified": "true",
		})
	})

	srv := &http.Server{Addr: ":8082", Handler: rateLimitMiddleware(mux)}

	go func() {
		logger.Info("listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error("server error", logx.Err(err))
		}
	}()

	if err := signalx.Wait(context.Background(), 5*time.Second, func(ctx context.Context) {
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("server shutdown error", logx.Err(err))
		}
		logger.Info("shutdown complete")
	}); err != nil {
		logger.Error("shutdown error", logx.Err(err))
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]string{"error": "internal error"}
	if ex, ok := errx.As(err); ok {
		resp["error"] = ex.Message
		resp["code"] = fmt.Sprintf("%s.%s", ex.Domain, ex.Code)
	}
	_ = json.NewEncoder(w).Encode(resp)
}
