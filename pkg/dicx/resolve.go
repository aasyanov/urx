package dicx

import (
	"context"
	"reflect"

	"github.com/aasyanov/urx/pkg/panix"
)

// resolveState tracks an in-flight singleton resolution.
type resolveState struct {
	done chan struct{}
	val  reflect.Value
	err  error
}

// resolve is the internal resolution entry point called by the generic Resolve/MustResolve functions.
// It uses a three-step flow:
//  1. capture provider/cache state under lock
//  2. execute constructor outside lock
//  3. commit singleton under lock
func (c *Container) resolve(t reflect.Type) (reflect.Value, error) {
	return c.resolveWithChain(t, nil)
}

// resolveWithChain resolves a type while tracking the dependency chain for
// cycle detection and diagnostic traces.
func (c *Container) resolveWithChain(t reflect.Type, chain []reflect.Type) (reflect.Value, error) {
	for _, ct := range chain {
		if ct == t {
			return reflect.Value{}, errCyclicDep(append(chain, t))
		}
	}

	c.mu.Lock()
	if val, ok := c.singletons[t]; ok {
		c.mu.Unlock()
		return val, nil
	}

	p, ok := c.providers[t]
	if !ok {
		c.mu.Unlock()
		return reflect.Value{}, errMissingDep(t, chain)
	}

	if p.lifetime == Singleton {
		if inFlight, ok := c.resolving[t]; ok {
			if c.dependsOnAnyLocked(t, chain) {
				c.mu.Unlock()
				return reflect.Value{}, errCyclicDep(append(chain, t))
			}
			c.mu.Unlock()
			<-inFlight.done
			if inFlight.err != nil {
				return reflect.Value{}, inFlight.err
			}
			return inFlight.val, nil
		}
		state := &resolveState{done: make(chan struct{})}
		c.resolving[t] = state
		c.mu.Unlock()
		return c.resolveSingleton(p, chain, state)
	}
	c.mu.Unlock()
	return c.resolveTransient(p, chain)
}

func (c *Container) resolveSingleton(p *provider, chain []reflect.Type, state *resolveState) (reflect.Value, error) {
	val, err := c.buildValue(p, chain)
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.resolving, p.outType)
	if err != nil {
		state.err = err
		close(state.done)
		return reflect.Value{}, err
	}

	// Another goroutine may have committed while we were building.
	if cached, ok := c.singletons[p.outType]; ok {
		state.val = cached
		close(state.done)
		return cached, nil
	}

	c.singletons[p.outType] = val
	c.order = append(c.order, p.outType)
	state.val = val
	close(state.done)
	return val, nil
}

func (c *Container) resolveTransient(p *provider, chain []reflect.Type) (reflect.Value, error) {
	return c.buildValue(p, chain)
}

func (c *Container) buildValue(p *provider, chain []reflect.Type) (reflect.Value, error) {
	chain = append(chain, p.outType)
	args := make([]reflect.Value, len(p.inTypes))
	for i, dep := range p.inTypes {
		val, err := c.resolveWithChain(dep, chain)
		if err != nil {
			return reflect.Value{}, err
		}
		args[i] = val
	}

	results, err := safeCall(p.ctor, args)
	if err != nil {
		return reflect.Value{}, errConstructorFailed(p.outType, err, chain[:len(chain)-1])
	}

	if p.hasError && !results[1].IsNil() {
		return reflect.Value{}, errConstructorFailed(p.outType, results[1].Interface().(error), chain[:len(chain)-1])
	}

	return results[0], nil
}

// dependsOnAnyLocked reports whether start transitively depends on any type in chain.
// c.mu must be held by the caller.
func (c *Container) dependsOnAnyLocked(start reflect.Type, chain []reflect.Type) bool {
	if len(chain) == 0 {
		return false
	}
	targets := make(map[reflect.Type]struct{}, len(chain))
	for _, t := range chain {
		targets[t] = struct{}{}
	}
	seen := make(map[reflect.Type]bool, len(c.providers))
	var walk func(reflect.Type) bool
	walk = func(cur reflect.Type) bool {
		if _, ok := targets[cur]; ok {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		p, ok := c.providers[cur]
		if !ok {
			return false
		}
		for _, dep := range p.inTypes {
			if walk(dep) {
				return true
			}
		}
		return false
	}
	for _, dep := range c.providers[start].inTypes {
		if walk(dep) {
			return true
		}
	}
	return false
}

// safeCall invokes fn with args, recovering from any constructor panic.
func safeCall(fn reflect.Value, args []reflect.Value) (results []reflect.Value, err error) {
	return panix.Safe[[]reflect.Value](context.Background(), "dicx.constructor", func(context.Context) ([]reflect.Value, error) {
		return fn.Call(args), nil
	})
}
