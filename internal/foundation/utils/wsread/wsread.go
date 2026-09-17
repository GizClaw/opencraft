// Package wsread reads workspace files under a byte cap. Tools that
// inline file bytes into a provider request (view_image,
// generate_image) share one rule: a workspace with bounded reads fails
// an oversized file without materializing it, and a backend that still
// returns more than the cap is rejected too.
package wsread

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// Capped reads one workspace file at most max bytes long. The optional
// bounded-read interface is preferred so an oversized file never lands
// in memory whole; workspaces without it fall back to a full read that
// is checked afterwards.
func Capped(
	ctx context.Context, ws workspace.Workspace, path string, max int64,
) ([]byte, error) {
	if max <= 0 {
		return nil, errdefs.Validationf(
			"workspace read cap must be positive, got %d", max)
	}
	if lr, ok := ws.(workspace.LimitedReader); ok {
		data, err := lr.ReadLimited(ctx, path, max)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > max {
			// Defensive: the interface rejects oversized files, but a
			// backend that returns more than it was asked for must not
			// reach a decoder either.
			return nil, errdefs.Validationf(
				"%s is %d bytes, over the %d-byte read cap",
				path, len(data), max)
		}
		return data, nil
	}
	data, err := ws.Read(ctx, path)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, errdefs.Validationf(
			"%s is %d bytes, over the %d-byte read cap",
			path, len(data), max)
	}
	return data, nil
}
