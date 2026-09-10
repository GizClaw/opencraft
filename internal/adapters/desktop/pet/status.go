package pet

import "time"

// RuntimeStatus is the pet window's self-report about the pack it
// mounted. The renderer validates the pack against the loaded .riv on
// mount; a failure used to leave a silently frozen character, so the
// result travels back to the settings diagnostics panel instead.
type RuntimeStatus struct {
	// PackID is the pack the window tried to mount.
	PackID string `json:"pack_id"`
	// Artboard/StateMachine/ViewModel echo what the pack asked for.
	Artboard     string `json:"artboard"`
	StateMachine string `json:"state_machine,omitempty"`
	ViewModel    string `json:"view_model,omitempty"`
	// OK is true when the pack matched the asset.
	OK bool `json:"ok"`
	// Missing lists every mismatch found, in the order it was found
	// ("artboard \"Pet\"", "view model property \"walking\"", ...).
	// Empty when OK.
	Missing []string `json:"missing,omitempty"`
	// Error carries a load/mount failure that prevented validation
	// (missing asset, decode error, runtime timeout).
	Error string `json:"error,omitempty"`
	// ReportedAt is stamped by the desktop when the status arrives.
	ReportedAt time.Time `json:"reported_at"`
}
