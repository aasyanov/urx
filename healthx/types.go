package healthx

// Status is the health state of a single component or of the system as a
// whole, as reported in a [Report].
type Status string

const (
	// StatusUp indicates the component or system is healthy.
	StatusUp Status = "up"

	// StatusDown indicates the component or system is unhealthy.
	StatusDown Status = "down"
)

// ComponentStatus is the outcome of a single registered component check,
// produced by [Checker.Readiness] and serialised into a [Report].
type ComponentStatus struct {
	// Status is StatusUp when the check returned nil, StatusDown otherwise.
	Status Status `json:"status"`

	// Error is the failure message when Status is StatusDown, empty otherwise.
	Error string `json:"error,omitempty"`

	// Duration is how long the check took, formatted via [time.Duration.String].
	Duration string `json:"duration"`
}

// Report is the aggregate result of a liveness or readiness probe. It is
// JSON-serialisable and written verbatim by the HTTP handlers.
type Report struct {
	// Status is StatusDown if any component is down or the system was marked
	// down via [Checker.MarkDown]; StatusUp otherwise.
	Status Status `json:"status"`

	// Components maps each registered component name to its check result. It
	// is nil for liveness reports and for readiness reports short-circuited
	// by [Checker.MarkDown].
	Components map[string]ComponentStatus `json:"components,omitempty"`

	// Duration is the total probe wall-clock time, formatted via
	// [time.Duration.String].
	Duration string `json:"duration"`
}

// CheckerStats is a point-in-time snapshot of [Checker] counters returned by
// [Checker.Stats].
type CheckerStats struct {
	// Registered is the number of component checks currently registered.
	Registered int `json:"registered"`

	// Down reports whether the system has been manually marked down via
	// [Checker.MarkDown].
	Down bool `json:"down"`

	// ReadinessChecks is the total number of [Checker.Readiness] invocations.
	ReadinessChecks uint64 `json:"readiness_checks"`

	// ReadinessFailures is the total number of [Checker.Readiness]
	// invocations that returned [StatusDown].
	ReadinessFailures uint64 `json:"readiness_failures"`
}
