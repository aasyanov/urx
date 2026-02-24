// Package dicx provides a production-grade dependency injection container for
// Go 1.24+ with generics-first API, lifecycle management, cyclic dependency
// detection, dependency trace in errors, and first-class [errx] integration.
//
// Register constructors with [Container.Provide], start the lifecycle with
// [Container.Start], and resolve dependencies with [Resolve] or [MustResolve]:
//
//	c := dicx.New()
//	c.Provide(NewConfig)
//	c.Provide(NewDB)
//	c.Provide(NewUserService)
//
//	if err := c.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer c.Stop(ctx)
//
//	svc := dicx.MustResolve[*UserService](c)
//
package dicx

import (
	"context"
	"reflect"
	"sync"

	"github.com/aasyanov/urx/pkg/errx"
)

// Container is a dependency injection container that manages constructor
// registration, singleton caching, lifecycle hooks, and dependency resolution.
//
// A Container is created with [New], populated with [Container.Provide],
// started with [Container.Start], and shut down with [Container.Stop].
// After Start, no new providers may be registered.
type Container struct {
	mu         sync.RWMutex
	providers  map[reflect.Type]*provider
	singletons map[reflect.Type]reflect.Value
	resolving  map[reflect.Type]*resolveState
	order      []reflect.Type
	started    bool
	starting   bool
	stopped    bool
}

// Stats holds a point-in-time snapshot of container state.
type Stats struct {
	Providers  int  `json:"providers"`
	Singletons int  `json:"singletons"`
	Started    bool `json:"started"`
	Stopped    bool `json:"stopped"`
}

// Stats returns a snapshot of container statistics.
func (c *Container) Stats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return Stats{
		Providers:  len(c.providers),
		Singletons: len(c.singletons),
		Started:    c.started,
		Stopped:    c.stopped,
	}
}

// IsClosed reports whether the container has been stopped.
func (c *Container) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stopped
}

// New creates an empty [Container] ready for provider registration.
func New() *Container {
	return &Container{
		providers:  make(map[reflect.Type]*provider),
		singletons: make(map[reflect.Type]reflect.Value),
		resolving:  make(map[reflect.Type]*resolveState),
	}
}

// Provide registers a constructor in the container. The constructor must be a
// function with the signature func(...deps) T or func(...deps) (T, error).
// Variadic functions are not allowed. The return type must not already be
// registered. Provide must be called before [Container.Start].
func (c *Container) Provide(constructor any, opts ...ProvideOption) error {
	p, err := newProvider(constructor, opts...)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started || c.starting {
		return errFrozen()
	}
	if _, exists := c.providers[p.outType]; exists {
		return errAlreadyProvided(p.outType)
	}
	c.providers[p.outType] = p
	return nil
}

// Start resolves all singleton providers and calls [Starter.Start] on each
// component that implements it, in dependency order. After Start returns
// successfully, the container is frozen: no new providers may be registered.
// A nil ctx is treated as [context.Background].
func (c *Container) Start(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return nil
	}
	if c.starting {
		c.mu.Unlock()
		return nil
	}
	c.starting = true

	singletonTypes := make([]reflect.Type, 0, len(c.providers))
	for _, p := range c.providers {
		if p.lifetime == Singleton {
			singletonTypes = append(singletonTypes, p.outType)
		}
	}
	c.mu.Unlock()

	for _, t := range singletonTypes {
		if _, err := c.resolve(t); err != nil {
			c.mu.Lock()
			c.starting = false
			c.mu.Unlock()
			return err
		}
	}

	type starterCall struct {
		t reflect.Type
		s Starter
	}
	c.mu.RLock()
	starterCalls := make([]starterCall, 0, len(c.order))
	for _, t := range c.order {
		val, ok := c.singletons[t]
		if !ok {
			continue
		}
		if s, ok := val.Interface().(Starter); ok {
			starterCalls = append(starterCalls, starterCall{
				t: t,
				s: s,
			})
		}
	}
	c.mu.RUnlock()

	for _, call := range starterCalls {
		if err := call.s.Start(ctx); err != nil {
			c.mu.Lock()
			c.starting = false
			c.mu.Unlock()
			return errx.Wrap(err, DomainDI, CodeLifecycleFailed,
				"Start failed for "+call.t.String(),
				errx.WithMeta("type", call.t.String()),
			)
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.started = true
	c.starting = false
	return nil
}

// Stop calls [Stopper.Stop] on each singleton that implements it, in reverse
// dependency order. Errors are aggregated via [errx.MultiError].
// A nil ctx is treated as [context.Background].
func (c *Container) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	c.stopped = true

	type stopperCall struct {
		t reflect.Type
		s Stopper
	}
	stopperCalls := make([]stopperCall, 0, len(c.order))
	for i := len(c.order) - 1; i >= 0; i-- {
		t := c.order[i]
		val, ok := c.singletons[t]
		if !ok {
			continue
		}
		if s, ok := val.Interface().(Stopper); ok {
			stopperCalls = append(stopperCalls, stopperCall{
				t: t,
				s: s,
			})
		}
	}
	c.mu.Unlock()

	me := errx.NewMulti()
	for _, call := range stopperCalls {
		if err := call.s.Stop(ctx); err != nil {
			me.Add(errx.Wrap(err, DomainDI, CodeLifecycleFailed,
				"Stop failed for "+call.t.String(),
				errx.WithMeta("type", call.t.String()),
			))
		}
	}
	return me.Err()
}
