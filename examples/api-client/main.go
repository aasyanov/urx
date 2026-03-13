// Example: Maximum URX — every resilience layer on a public API.
//
// Calls https://api.github.com (no auth needed) with the full URX stack:
//
//	ratex       — 5 req/s rate limit (don't abuse the API)
//	lrux        — in-memory cache with 1 min TTL
//	shedx       — drop low-priority requests under load
//	bulkx       — max 3 concurrent outbound calls
//	circuitx    — open after 3 failures, reset after 10s
//	retryx      — 3 attempts, 500ms exponential backoff
//	toutx       — 5s per-request deadline
//	fallx       — return cached data when everything fails
//	adaptx      — auto-tune concurrency limit
//	ctxx+logx   — trace IDs in every log line
//	errx        — structured errors everywhere
//	panix       — panic recovery (wrapped inside Execute/Do)
//	healthx     — /healthz endpoint
//	signalx     — graceful shutdown on Ctrl+C
//
// Run:
//
//	go run ./examples/api-client
//
// Then watch the logs. Press Ctrl+C to stop.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aasyanov/urx/pkg/adaptx"
	"github.com/aasyanov/urx/pkg/bulkx"
	"github.com/aasyanov/urx/pkg/circuitx"
	"github.com/aasyanov/urx/pkg/ctxx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/fallx"
	"github.com/aasyanov/urx/pkg/healthx"
	"github.com/aasyanov/urx/pkg/logx"
	"github.com/aasyanov/urx/pkg/lrux"
	"github.com/aasyanov/urx/pkg/ratex"
	"github.com/aasyanov/urx/pkg/retryx"
	"github.com/aasyanov/urx/pkg/shedx"
	"github.com/aasyanov/urx/pkg/signalx"
	"github.com/aasyanov/urx/pkg/toutx"
)

type GitHubRepo struct {
	Name  string `json:"name"`
	Stars int    `json:"stargazers_count"`
	Desc  string `json:"description"`
}

func main() {
	// --- Infrastructure ---

	logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))
	slog.SetDefault(logger)

	health := healthx.New(healthx.WithTimeout(2 * time.Second))
	health.Register("github-api", func(ctx context.Context) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, "https://api.github.com", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	})

	// --- Resilience stack ---

	rl := ratex.New(ratex.WithRate(5), ratex.WithBurst(2))

	cache := lrux.New[string, []GitHubRepo](
		lrux.WithCapacity[string, []GitHubRepo](50),
		lrux.WithTTL[string, []GitHubRepo](1*time.Minute),
	)
	defer cache.Close()

	shed := shedx.New(shedx.WithCapacity(20), shedx.WithThreshold(0.8))
	defer shed.Close()

	bh := bulkx.New(bulkx.WithMaxConcurrent(3), bulkx.WithTimeout(10*time.Second))
	cb := circuitx.New(circuitx.WithMaxFailures(3), circuitx.WithResetTimeout(10*time.Second))

	al := adaptx.New(
		adaptx.WithAlgorithm(adaptx.AIMD),
		adaptx.WithInitialLimit(5),
		adaptx.WithMinLimit(1),
		adaptx.WithMaxLimit(10),
	)
	defer al.Close()

	fb := fallx.New(fallx.WithCached[[]GitHubRepo](5*time.Minute, 100))
	defer fb.Close()

	// --- The call function: all layers composed ---

	fetchRepos := func(ctx context.Context, org string, priority shedx.Priority) ([]GitHubRepo, error) {
		ctx = ctxx.WithTrace(ctx)
		ctx = ctxx.WithSpan(ctx)
		ctx = logx.WithLogger(ctx, logger)
		log := logx.FromContext(ctx)

		if cached, ok := cache.Get(org); ok {
			log.Debug("cache hit", slog.String("org", org))
			return cached, nil
		}

		if err := rl.Wait(ctx); err != nil {
			return nil, errx.Wrap(err, "API", "RATE_LIMITED", "rate limit exceeded")
		}

		result, err := fb.DoWithKey(ctx, org, func(ctx context.Context) ([]GitHubRepo, error) {
			return shedx.Execute(shed, ctx, priority, func(ctx context.Context, sc shedx.ShedController) ([]GitHubRepo, error) {
				return adaptx.Do(al, ctx, func(ctx context.Context, ac adaptx.AdaptController) ([]GitHubRepo, error) {
					return bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) ([]GitHubRepo, error) {
						return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) ([]GitHubRepo, error) {
							return retryx.Do(ctx, func(rc retryx.RetryController) ([]GitHubRepo, error) {
								return toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) ([]GitHubRepo, error) {
									url := fmt.Sprintf("https://api.github.com/orgs/%s/repos?per_page=5&sort=stars", org)
									req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
									req.Header.Set("Accept", "application/vnd.github.v3+json")

									resp, err := http.DefaultClient.Do(req)
									if err != nil {
										return nil, err
									}
									defer resp.Body.Close()

									if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
										cc.SkipFailure()
										return nil, errx.New("GITHUB", "RATE_LIMITED",
											fmt.Sprintf("GitHub rate limit (%d)", resp.StatusCode))
									}

									if resp.StatusCode >= 500 {
										return nil, errx.New("GITHUB", "SERVER_ERROR",
											fmt.Sprintf("GitHub %d", resp.StatusCode))
									}

									if resp.StatusCode >= 400 {
										rc.Abort()
										return nil, errx.New("GITHUB", "CLIENT_ERROR",
											fmt.Sprintf("GitHub %d", resp.StatusCode),
											errx.WithRetry(errx.RetryUnsafe))
									}

									body, _ := io.ReadAll(resp.Body)
									var repos []GitHubRepo
									if err := json.Unmarshal(body, &repos); err != nil {
										rc.Abort()
										return nil, errx.Wrap(err, "GITHUB", "PARSE_ERROR", "bad JSON")
									}

									log.Info("fetched",
										slog.String("org", org),
										slog.Int("repos", len(repos)),
										slog.Int("attempt", rc.Number()),
										slog.Int("active", bc.Active()),
										slog.String("circuit", cc.State().String()),
										slog.Float64("load", sc.Load()),
										slog.Int("adapt_limit", ac.Limit()),
									)

									return repos, nil
								})
							}, retryx.WithMaxAttempts(3), retryx.WithBackoff(500*time.Millisecond))
						})
					})
				})
			})
		})

		if err == nil {
			cache.Set(org, result)
		}
		return result, err
	}

	// --- Run concurrent requests ---

	orgs := []string{"golang", "google", "microsoft", "github", "kubernetes"}

	var wg sync.WaitGroup
	for i, org := range orgs {
		wg.Add(1)
		go func(org string, idx int) {
			defer wg.Done()

			priority := shedx.PriorityNormal
			if idx == 0 {
				priority = shedx.PriorityCritical
			}

			repos, err := fetchRepos(context.Background(), org, priority)
			if err != nil {
				if ex, ok := errx.As(err); ok {
					logger.Error("failed",
						slog.String("org", org),
						slog.String("domain", ex.Domain),
						slog.String("code", ex.Code),
						logx.Err(err),
					)
				}
				return
			}

			for _, r := range repos {
				desc := r.Desc
				if len(desc) > 60 {
					desc = desc[:60] + "..."
				}
				fmt.Printf("  [%s] %-30s ⭐ %-6d %s\n", org, r.Name, r.Stars, desc)
			}
		}(org, i)
	}

	wg.Wait()

	// --- Print stats ---

	fmt.Println("\n--- Stats ---")
	cacheStats := cache.Stats()
	fmt.Printf("Cache:    hits=%d misses=%d rate=%.0f%%\n", cacheStats.Hits, cacheStats.Misses, cacheStats.HitRate*100)
	rlStats := rl.Stats()
	fmt.Printf("RateLimit: allowed=%d limited=%d\n", rlStats.Allowed, rlStats.Limited)
	fmt.Printf("Shedder:   load=%.0f%%\n", shed.Load()*100)

	report := health.Readiness(context.Background())
	fmt.Printf("Health:    status=%s\n", report.Status)

	// --- Keep alive for health probes (optional) ---

	go func() {
		mux := http.NewServeMux()
		mux.Handle("/healthz", health.LiveHandler())
		mux.Handle("/readyz", health.ReadyHandler())
		logger.Info("health probes on :8081")
		if err := http.ListenAndServe(":8081", mux); err != nil {
			logger.Error("health server error", logx.Err(err))
		}
	}()

	if err := signalx.Wait(context.Background(), 5*time.Second, func(ctx context.Context) {
		logger.Info("shutdown complete")
	}); err != nil {
		logger.Error("shutdown error", logx.Err(err))
	}
}
