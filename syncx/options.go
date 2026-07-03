package syncx

const (
	// defaultLimit is the concurrency limit applied when [WithLimit] is not
	// supplied. Zero means unlimited.
	defaultLimit = 0
)

// GroupOption configures a [Group] created with [NewGroup].
type GroupOption func(*groupConfig)

type groupConfig struct {
	limit int
}

// WithLimit sets the maximum number of goroutines that may run concurrently
// within the group. [Group.Go] blocks once the limit is reached until a slot
// frees up.
//
// Default: unlimited. A value <= 0 means unlimited.
func WithLimit(n int) GroupOption {
	return func(c *groupConfig) {
		if n > 0 {
			c.limit = n
		}
	}
}

func newGroupConfig(opts []GroupOption) groupConfig {
	cfg := groupConfig{limit: defaultLimit}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}
