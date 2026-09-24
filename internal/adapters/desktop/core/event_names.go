// UI event names shared by every Shell.Emit call site.
//
// They are one half of a cross-language contract: the frontend half is
// frontend/src/lib/events.ts. TestEventNamesMatchFrontendMap parses that
// file, so the two lists cannot drift silently. The values are wire
// contract — renaming one breaks any frontend build older than the
// backend — which is why emit sites must use these constants instead of
// literals.
package core

// UIEventChannel is the Wails event every Shell.Emit-delivered UI event
// travels on. The payload envelope is {"type": <name>, "data": ...}.
// The menu channel and the pet window's state channel are not UI events
// and are declared separately (MenuCommandEvent, EventPetState).
const UIEventChannel = "opencraft:ui"

// UI event type names, grouped the way a session produces them.
const (
	// Lifecycle and runtime status.
	EventReady  = "ready"
	EventFatal  = "fatal"
	EventStatus = "status"

	// Turn progress.
	EventUsage        = "usage"
	EventStream       = "stream"
	EventArtifact     = "artifact"
	EventInteract     = "interact"
	EventResolved     = "resolved"
	EventSteerPending = "steer_pending"
	EventTurnEnd      = "turn_end"

	// Session data and automations.
	EventSessionUpdated       = "session_updated"
	EventAutomationChanged    = "automation_changed"
	EventAutomationRun        = "automation_run"
	EventAutomationRunStarted = "automation_run_started"
	EventManagedRestored      = "managed_restored"

	// Workspace and configuration.
	EventGitChanged       = "git_changed"
	EventInferenceChanged = "inference_changed"
	EventTelemetryChanged = "telemetry_changed"

	// Pet feed.
	EventPetPacksChanged    = "pet:packs_changed"
	EventPetSettingsChanged = "pet:settings_changed"
	EventPetRuntimeStatus   = "pet:runtime_status"
)

// EventPetState is the pet window's own state channel: the pet loop
// emits it straight to the pet webview, so it never travels on
// UIEventChannel.
const EventPetState = "pet:state"
