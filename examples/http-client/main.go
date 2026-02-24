// Example: Resilient HTTP client using URX composition.
//
// Demonstrates: bulkx → circuitx → retryx → toutx layered around
// a standard net/http call.
//
// Run:
//
//	go run ./examples/http-client
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/aasyanov/urx/pkg/bulkx"
	"github.com/aasyanov/urx/pkg/circuitx"
	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/logx"
	"github.com/aasyanov/urx/pkg/retryx"
	"github.com/aasyanov/urx/pkg/toutx"
)

func main() {
	logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))

	bh := bulkx.New(bulkx.WithMaxConcurrent(5))
	cb := circuitx.New(circuitx.WithMaxFailures(3), circuitx.WithResetTimeout(10*time.Second))

	ctx := ctxx.WithTrace(context.Background())
	ctx = logx.WithLogger(ctx, logger)

	type result struct {
		status int
		body   string
	}

	resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*result, error) {
		return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*result, error) {
			return retryx.Do(ctx, func(rc retryx.RetryController) (*result, error) {
				return toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) (*result, error) {
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://httpbin.org/get", nil)
					httpResp, err := http.DefaultClient.Do(req)
					if err != nil {
						return nil, err
					}
					defer httpResp.Body.Close()
					body, _ := io.ReadAll(httpResp.Body)

					if httpResp.StatusCode == http.StatusTooManyRequests {
						cc.SkipFailure()
						return nil, errx.New("HTTP", "RATE_LIMITED", "429 from upstream")
					}

					if httpResp.StatusCode >= 500 {
						return nil, errx.New("HTTP", "SERVER_ERROR",
							fmt.Sprintf("status %d", httpResp.StatusCode))
					}

					if httpResp.StatusCode >= 400 {
						rc.Abort()
						return nil, errx.New("HTTP", "CLIENT_ERROR",
							fmt.Sprintf("status %d", httpResp.StatusCode),
							errx.WithRetry(errx.RetryUnsafe))
					}

					return &result{status: httpResp.StatusCode, body: string(body[:80])}, nil
				})
			}, retryx.WithMaxAttempts(3), retryx.WithBackoff(500*time.Millisecond))
		})
	})

	if err != nil {
		logx.FromContext(ctx).Error("request failed", logx.Err(err))
		os.Exit(1)
	}

	logx.FromContext(ctx).Info("success",
		slog.Int("status", resp.status),
		slog.String("body_preview", resp.body),
	)
}
