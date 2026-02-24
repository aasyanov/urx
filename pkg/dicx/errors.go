package dicx

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainDI is the [errx] domain for all dependency injection errors.
const DomainDI = "DI"

// --- Code constants ---

const (
	// CodeCyclicDep indicates a circular dependency was detected during resolution.
	CodeCyclicDep = "CYCLIC_DEP"
	// CodeMissingDep indicates a required dependency type was not registered.
	CodeMissingDep = "MISSING_DEP"
	// CodeBadConstructor indicates the constructor function has an invalid signature.
	CodeBadConstructor = "BAD_CONSTRUCTOR"
	// CodeConstructorFailed indicates the constructor returned an error during resolution.
	CodeConstructorFailed = "CONSTRUCTOR_FAILED"
	// CodeAlreadyProvided indicates the same type was registered more than once.
	CodeAlreadyProvided = "ALREADY_PROVIDED"
	// CodeFrozen indicates Provide was called after the container was started.
	CodeFrozen = "FROZEN"
	// CodeLifecycleFailed indicates a Start or Stop hook returned an error.
	CodeLifecycleFailed = "LIFECYCLE_FAILED"
)

// --- Error constructors ---

// errBadConstructor builds a structured error for an invalid constructor signature.
func errBadConstructor(msg string) *errx.Error {
	return errx.New(DomainDI, CodeBadConstructor, msg)
}

// errAlreadyProvided builds a structured error when a type is registered twice.
func errAlreadyProvided(t reflect.Type) *errx.Error {
	return errx.New(DomainDI, CodeAlreadyProvided,
		fmt.Sprintf("%v already registered", t))
}

// errFrozen builds a structured error when Provide is called after Start.
func errFrozen() *errx.Error {
	return errx.New(DomainDI, CodeFrozen, "container is started; Provide is no longer allowed")
}

// errMissingDep builds a structured error for an unregistered dependency type.
func errMissingDep(t reflect.Type, chain []reflect.Type) *errx.Error {
	return errx.New(DomainDI, CodeMissingDep,
		fmt.Sprintf("%v not registered", t),
		errx.WithMeta("type", t.String(), "trace", formatChain(chain, t)),
	)
}

// errCyclicDep builds a structured error when a circular dependency is detected.
func errCyclicDep(chain []reflect.Type) *errx.Error {
	return errx.New(DomainDI, CodeCyclicDep,
		"cyclic dependency detected",
		errx.WithMeta("cycle", formatCycle(chain)),
	)
}

// errConstructorFailed wraps a constructor error with the dependency trace.
func errConstructorFailed(t reflect.Type, cause error, chain []reflect.Type) *errx.Error {
	return errx.Wrap(cause, DomainDI, CodeConstructorFailed,
		fmt.Sprintf("constructor for %v failed", t),
		errx.WithMeta("type", t.String(), "trace", formatChain(chain, t)),
	)
}

// --- Formatting helpers ---

// formatChain renders the dependency resolution path as "A -> B -> C".
func formatChain(chain []reflect.Type, tail reflect.Type) string {
	if len(chain) == 0 {
		return tail.String()
	}
	var sb strings.Builder
	for _, t := range chain {
		sb.WriteString(t.String())
		sb.WriteString(" -> ")
	}
	sb.WriteString(tail.String())
	return sb.String()
}

// formatCycle renders a circular dependency path as "A -> B -> A".
func formatCycle(chain []reflect.Type) string {
	if len(chain) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, t := range chain {
		if i > 0 {
			sb.WriteString(" -> ")
		}
		sb.WriteString(t.String())
	}
	sb.WriteString(" -> ")
	sb.WriteString(chain[0].String())
	return sb.String()
}
