// Package resourcedep provides the shared resource-dependency extraction
// helper used by opencraft's deploy factories.
package resourcedep

import (
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
)

// Required returns the named dependency asserted to T. A missing dep
// or a type mismatch is a validation error prefixed with component.
func Required[T any](in resource.Input, component, name string) (T, error) {
	var zero T
	dep, ok := in.Dep(name)
	if !ok {
		return zero, errdefs.Validationf(
			"%s: dep %q is required", component, name)
	}
	value, ok := dep.(T)
	if !ok {
		return zero, errdefs.Validationf(
			"%s: dep %q is %T, want %T", component, name, dep, zero)
	}
	return value, nil
}
