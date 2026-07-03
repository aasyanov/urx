package healthx

import (
	"encoding/json"
	"net/http"
)

const (
	// routeHealthz and routeLivez are the liveness probe paths registered by
	// [Checker.RegisterHandlers]; routeReadyz is the readiness path.
	routeHealthz = "/healthz"
	routeLivez   = "/livez"
	routeReadyz  = "/readyz"
)

// LiveHandler returns an [http.Handler] for the liveness probe. It responds
// with 200 and the [Report] JSON when up, or 503 when marked down via
// [Checker.MarkDown].
func (c *Checker) LiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeReport(w, c.Liveness(r.Context()))
	})
}

// ReadyHandler returns an [http.Handler] for the readiness probe. It runs all
// registered checks and responds with 200 and the [Report] JSON when every
// check passes, or 503 when any check fails or the system is marked down.
func (c *Checker) ReadyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeReport(w, c.Readiness(r.Context()))
	})
}

// RegisterHandlers registers the standard Kubernetes probe endpoints on mux:
// "/healthz" and "/livez" for liveness, "/readyz" for readiness.
// Panics if mux is nil.
func (c *Checker) RegisterHandlers(mux *http.ServeMux) {
	if mux == nil {
		panic("healthx: RegisterHandlers mux must not be nil")
	}
	live := c.LiveHandler()
	mux.Handle(routeHealthz, live)
	mux.Handle(routeLivez, live)
	mux.Handle(routeReadyz, c.ReadyHandler())
}

// writeReport serialises report as JSON and sets the HTTP status code: 200
// for [StatusUp], 503 for [StatusDown].
func writeReport(w http.ResponseWriter, report Report) {
	w.Header().Set("Content-Type", "application/json")
	code := http.StatusOK
	if report.Status == StatusDown {
		code = http.StatusServiceUnavailable
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(report)
}
