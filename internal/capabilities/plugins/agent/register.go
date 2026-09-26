package agent

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/resource"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins"
	"github.com/GizClaw/opencraft/internal/capabilities/plugins/runtime"
)

// ResourceKind is the deploy resource kind of the shared plugin host.
const ResourceKind = "opencraft.plugins"

// Host is the read-side view of the plugin registry for agent-facing
// capabilities. A nil store yields an empty host (CLI / tests without
// a desktop plugin root).
type Host struct {
	ctx    context.Context
	store  *plugins.Store
	cap    *runtime.Manager
	once   sync.Once
	cached []pluginEntry
}

// NewHost wraps an installed plugin store and its capability runtime.
func NewHost(ctx context.Context, store *plugins.Store, cap *runtime.Manager) *Host {
	return &Host{ctx: ctx, store: store, cap: cap}
}

// NewEmpty returns a host with no plugins. It is used by runtimes
// that did not receive a desktop plugin root.
func NewEmpty() *Host { return &Host{} }

// Empty reports whether the host has no plugin store.
func (h *Host) Empty() bool { return h.store == nil }

// Factory builds the opencraft.plugins resource. Host may be nil for
// runtimes without a desktop plugin root; the resource still exists so
// skills/hooks/tool sources can declare an optional dependency on it.
type Factory struct {
	Host *Host
}

var _ resource.Factory = Factory{}

// Spec implements resource.Factory.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "local"}
}

// New implements resource.Factory.
func (f Factory) New(_ context.Context, _ resource.Input) (any, error) {
	if f.Host == nil {
		return NewEmpty(), nil
	}
	return f.Host, nil
}
