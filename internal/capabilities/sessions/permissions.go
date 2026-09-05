package sessions

import (
	"context"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/foundation/profile"
)

// Mode is the per-session sandbox permission mode.
type Mode string

const (
	// ModeWorkspace confines commands and files to the workspace and
	// asks the user before running unlisted commands (the default).
	ModeWorkspace Mode = "workspace"
	// ModeReadOnly keeps the workspace root read-only for every command
	// (OS-enforced by the seatbelt/bwrap backend); known read-only
	// commands run without approval, everything else asks the user.
	ModeReadOnly Mode = "read-only"
	// ModeYOLO disables the sandbox for the session: commands run on
	// the host with the full environment, no approval prompts, and
	// file tools may reach any path.
	ModeYOLO Mode = "yolo"
)

// IsYOLO reports whether m is the unconfined mode.
func (m Mode) IsYOLO() bool { return m == ModeYOLO }

// IsReadOnly reports whether m keeps the workspace root read-only.
func (m Mode) IsReadOnly() bool { return m == ModeReadOnly }

// SetMode persists the sandbox mode for the session.
func (s *Store) SetMode(ctx context.Context, id string, mode Mode) error {
	switch mode {
	case ModeWorkspace, ModeReadOnly, ModeYOLO:
	default:
		return errdefs.Validationf(
			"sessions: unknown permission mode %q", mode)
	}
	if profile.YoloOnly() && mode != ModeYOLO {
		return errdefs.Validationf(
			"sessions: only yolo sandbox mode is available in this build")
	}
	if s.db != nil {
		if err := s.db.SetMode(ctx, id, string(mode)); err != nil {
			return err
		}
	}
	return nil
}

// Mode returns the persisted sandbox mode for the session, defaulting
// to workspace when the session has no stored mode.
func (s *Store) Mode(ctx context.Context, id string) (Mode, error) {
	// The yoloonly build pins the resolution point every sandbox and
	// tool policy reads from: legacy rows and callers that still pass
	// read-only/workspace must never produce a confined session.
	if profile.YoloOnly() {
		return ModeYOLO, nil
	}
	if s.db == nil {
		return ModeWorkspace, nil
	}
	stored, err := s.db.Mode(ctx, id)
	if err != nil {
		return ModeWorkspace, err
	}
	switch Mode(stored) {
	case ModeWorkspace, ModeReadOnly, ModeYOLO:
		return Mode(stored), nil
	default:
		return ModeWorkspace, nil
	}
}
