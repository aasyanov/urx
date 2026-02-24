package dicx

import "reflect"

// Resolve obtains a dependency of type T from the container (cached for Singleton,
// newly constructed for Transient). If the type has not been registered or its
// constructor fails, a structured [errx.Error] is returned with a dependency trace.
func Resolve[T any](c *Container) (T, error) {
	val, err := c.resolve(reflect.TypeFor[T]())
	if err != nil {
		var zero T
		return zero, err
	}
	return val.Interface().(T), nil
}

// MustResolve is like [Resolve] but panics on error. Use only in application
// bootstrap code where a missing dependency is a fatal configuration mistake.
func MustResolve[T any](c *Container) T {
	v, err := Resolve[T](c)
	if err != nil {
		panic("dicx: " + err.Error())
	}
	return v
}
