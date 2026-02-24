// Package busx provides a thread-safe, synchronous, in-process event bus for
// industrial Go services.
//
// Handlers are invoked synchronously in the caller's goroutine. Each handler
// is wrapped with [panix.Safe] for panic recovery; panicked handlers produce
// structured [errx.Error] values aggregated via [errx.MultiError].
//
//	b := busx.New()
//
//	id, _ := b.Subscribe("user.created", func(ctx context.Context, event string, payload any) {
//	    u := payload.(*User)
//	    sendWelcomeEmail(ctx, u)
//	})
//
//	b.Publish(ctx, "user.created", newUser)
//	b.Unsubscribe(id)
//	b.Close()
//
package busx

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

// --- Types ---

// HandlerFunc is the signature for event handlers.
// The payload is a single [any] value; callers wrap structured data in a
// struct or slice as needed.
type HandlerFunc func(ctx context.Context, event string, payload any)

// SubscriptionID uniquely identifies a subscription returned by [Bus.Subscribe].
type SubscriptionID uint64

// subscription pairs a handler with its unique ID.
type subscription struct {
	id SubscriptionID
	fn HandlerFunc
}

// --- Configuration ---

// config holds event bus parameters.
type config struct {
	onError func(event string, err error)
}

// defaultConfig returns zero-value bus defaults.
func defaultConfig() config { return config{} }

// Option configures [New] behavior.
type Option func(*config)

// WithOnError sets a callback invoked for each handler error during Publish.
func WithOnError(fn func(event string, err error)) Option {
	return func(c *config) { c.onError = fn }
}

// --- Bus ---

// Bus is a thread-safe, synchronous event bus. Create one with [New],
// subscribe handlers with [Bus.Subscribe], publish events with [Bus.Publish],
// and shut down with [Bus.Close].
type Bus struct {
	cfg       config
	mu        sync.RWMutex
	subs      map[string][]subscription
	index     map[SubscriptionID]string // id -> event name for O(1) unsubscribe
	nextID    atomic.Uint64
	published atomic.Uint64
	delivered atomic.Uint64
	panics    atomic.Uint64
	closed    atomic.Bool
}

// New creates an empty [Bus] ready for subscriptions.
func New(opts ...Option) *Bus {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Bus{
		cfg:   cfg,
		subs:  make(map[string][]subscription),
		index: make(map[SubscriptionID]string),
	}
}

// --- Subscribe / Unsubscribe ---

// Subscribe registers a handler for the given event and returns a unique
// [SubscriptionID] that can be passed to [Bus.Unsubscribe].
// Returns an error if the bus is closed or the handler is nil.
func (b *Bus) Subscribe(event string, fn HandlerFunc) (SubscriptionID, error) {
	if b.closed.Load() {
		return 0, errClosed("Subscribe")
	}
	if fn == nil {
		return 0, errNilHandler()
	}

	id := SubscriptionID(b.nextID.Add(1))

	b.mu.Lock()
	b.subs[event] = append(b.subs[event], subscription{id: id, fn: fn})
	b.index[id] = event
	b.mu.Unlock()

	return id, nil
}

// Unsubscribe removes the subscription identified by id. It uses an internal
// index to locate the event in O(1), then removes the handler via swap-delete.
// Returns true if a subscription was actually removed; false if the id does not
// exist.
func (b *Bus) Unsubscribe(id SubscriptionID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	event, ok := b.index[id]
	if !ok {
		return false
	}
	delete(b.index, id)

	subs := b.subs[event]
	for i, s := range subs {
		if s.id == id {
			last := len(subs) - 1
			subs[i] = subs[last]
			subs[last] = subscription{}
			subs = subs[:last]
			if len(subs) == 0 {
				delete(b.subs, event)
			} else {
				b.subs[event] = subs
			}
			return true
		}
	}
	return false
}

// --- Publish ---

// Publish invokes all handlers subscribed to event synchronously in the
// caller's goroutine. Each handler is wrapped with [panix.Safe]; if any
// handler panics, the panic is recovered and the resulting error is collected.
// Returns an [*errx.MultiError] if one or more handlers panicked, nil otherwise.
// Returns an error immediately if the bus is closed.
func (b *Bus) Publish(ctx context.Context, event string, payload any) error {
	if b.closed.Load() {
		return errClosed("Publish")
	}

	b.mu.RLock()
	handlers := make([]subscription, len(b.subs[event]))
	copy(handlers, b.subs[event])
	b.mu.RUnlock()

	if len(handlers) == 0 {
		return nil
	}

	b.published.Add(1)
	me := &errx.MultiError{}
	for _, s := range handlers {
		fn := s.fn
		_, err := panix.Safe[struct{}](ctx, "busx.handler", func(ctx context.Context) (struct{}, error) {
			fn(ctx, event, payload)
			return struct{}{}, nil
		})
		if err != nil {
			b.panics.Add(1)
			wrapped := errPublishFailed(event, err)
			me.Add(wrapped)
			if b.cfg.onError != nil {
				b.cfg.onError(event, wrapped)
			}
		} else {
			b.delivered.Add(1)
		}
	}
	return me.Err()
}

// --- Introspection ---

// Subscribers returns the number of handlers registered for the given event.
// Returns 0 if the event does not exist or the bus is closed.
func (b *Bus) Subscribers(event string) int {
	if b.closed.Load() {
		return 0
	}

	b.mu.RLock()
	n := len(b.subs[event])
	b.mu.RUnlock()

	return n
}

// Events returns a slice of all event names that have at least one subscriber.
// Returns nil if the bus is closed.
func (b *Bus) Events() []string {
	if b.closed.Load() {
		return nil
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	events := make([]string, 0, len(b.subs))
	for event := range b.subs {
		events = append(events, event)
	}
	return events
}

// --- Statistics ---

// Stats holds a point-in-time snapshot of event bus counters.
type Stats struct {
	Events        int    `json:"events"`
	Subscriptions int    `json:"subscriptions"`
	Published     uint64 `json:"published"`
	Delivered     uint64 `json:"delivered"`
	Panics        uint64 `json:"panics"`
}

// Stats returns a snapshot of event bus statistics.
func (b *Bus) Stats() Stats {
	b.mu.RLock()
	events := len(b.subs)
	subs := 0
	for _, s := range b.subs {
		subs += len(s)
	}
	b.mu.RUnlock()

	return Stats{
		Events:        events,
		Subscriptions: subs,
		Published:     b.published.Load(),
		Delivered:     b.delivered.Load(),
		Panics:        b.panics.Load(),
	}
}

// ResetStats zeroes all counters.
func (b *Bus) ResetStats() {
	b.published.Store(0)
	b.delivered.Store(0)
	b.panics.Store(0)
}

// --- Lifecycle ---

// Close shuts down the bus, preventing new subscriptions and publications.
// All existing subscriptions are cleared to allow garbage collection.
// Close is idempotent.
func (b *Bus) Close() {
	if b.closed.Swap(true) {
		return
	}

	b.mu.Lock()
	clear(b.subs)
	clear(b.index)
	b.mu.Unlock()
}

// IsClosed reports whether the bus has been closed.
func (b *Bus) IsClosed() bool {
	return b.closed.Load()
}
