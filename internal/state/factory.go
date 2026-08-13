package state

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
)

// ResourceKind is the deploy resource kind for opencraft state stores.
const ResourceKind = "state"

// Factory builds a SQLite state store from deploy settings. DefaultPath
// is used when the document does not set an explicit path; the
// application injects it (e.g. ~/.opencraft/opencraft.db).
type Factory struct {
	DefaultPath string
}

var _ resource.Factory = Factory{}

// Spec declares the resource shape: kind state, impl sqlite.
func (Factory) Spec() resource.Spec {
	return resource.Spec{Kind: ResourceKind, Impl: "sqlite"}
}

type settings struct {
	Path string `json:"path"`
}

// New opens the SQLite store at the configured path.
func (f Factory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[settings](in.Settings)
	if err != nil {
		return nil, err
	}
	if s.Path == "" {
		s.Path = f.DefaultPath
	}
	if s.Path == "" {
		return nil, errdefs.Validationf("state: path is required")
	}
	return Open(s.Path)
}
