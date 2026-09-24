// UI event contract with the Go backend.
//
// The wire half lives in internal/adapters/desktop/core/event_names.go,
// and core's TestEventNamesMatchFrontendMap parses this file, so the two
// lists must stay equal — same values, same set.
//
// Components register on UIEventChannel and compare the envelope's type
// against UIEventType instead of spelling the wire string at the call
// site, so a rename has exactly one place to change per side.

/** The channel every backend UI event arrives on. */
export const UIEventChannel = 'opencraft:ui';

/** The native menu channel (Go: core.MenuCommandEvent). */
export const MenuCommandChannel = 'opencraft:menu';

/** The pet window's own state channel (Go: core.EventPetState). */
export const PetStateChannel = 'pet:state';

/**
 * Event type names delivered on UIEventChannel, grouped the way the Go
 * registry groups them: lifecycle, turn progress, session data and
 * automations, workspace and config, pet feed.
 */
export const UIEventType = {
  // Lifecycle and runtime status.
  ready: 'ready',
  fatal: 'fatal',
  status: 'status',

  // Turn progress.
  usage: 'usage',
  stream: 'stream',
  artifact: 'artifact',
  interact: 'interact',
  resolved: 'resolved',
  steerPending: 'steer_pending',
  turnEnd: 'turn_end',

  // Session data and automations.
  sessionUpdated: 'session_updated',
  automationChanged: 'automation_changed',
  automationRun: 'automation_run',
  automationRunStarted: 'automation_run_started',
  managedRestored: 'managed_restored',

  // Workspace and configuration.
  gitChanged: 'git_changed',
  inferenceChanged: 'inference_changed',
  telemetryChanged: 'telemetry_changed',

  // Pet feed (the pet window's own events travel on PetStateChannel).
  petPacksChanged: 'pet:packs_changed',
  petSettingsChanged: 'pet:settings_changed',
  petRuntimeStatus: 'pet:runtime_status',
} as const;
