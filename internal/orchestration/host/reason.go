package host

import "context"

// AssemblyReason names what asked for a runtime assembly (or for the
// invalidation that precedes one). Assemblies are the expensive part of
// a workspace open — document load, resource build, discovery, MCP — so
// every one of them logs a line naming its caller. Without it a rebuild
// storm can only be guessed at from neighboring log lines, which is
// exactly how the 2026-09 latency review had to work.
type AssemblyReason string

const (
	// ReasonUnknown is what an untagged context reports. A headless run
	// and a test both assemble once and never churn, so they stay here.
	ReasonUnknown AssemblyReason = "unknown"
	// ReasonStartup is the first assembly of the process.
	ReasonStartup AssemblyReason = "startup"
	// ReasonWorkspaceOpen is a workspace switch (or reopen) that rebinds
	// the active Host.
	ReasonWorkspaceOpen AssemblyReason = "workspace_open"
	// ReasonSettingsSave is any settings write that routes through the
	// document reload path.
	ReasonSettingsSave AssemblyReason = "settings_save"
	// ReasonPluginChange is a plugin install, update, enable or disable.
	ReasonPluginChange AssemblyReason = "plugin_change"
	// ReasonToolOptionsSave is a tool-options write.
	ReasonToolOptionsSave AssemblyReason = "tooloptions_save"
	// ReasonInferenceChange is an inference/provider write.
	ReasonInferenceChange AssemblyReason = "inference_change"
	// ReasonPathSave is a process PATH override write.
	ReasonPathSave AssemblyReason = "path_save"
	// ReasonProbeSave is a provider round-trip probe switch that reloads
	// so provider clients rebuild against (or away from) the wrapped
	// transport.
	ReasonProbeSave AssemblyReason = "probe_save"
	// ReasonRetryAfterDrain is the deferred rebuild armed when a stale
	// Host was acquired while it was still draining runs.
	ReasonRetryAfterDrain AssemblyReason = "retry_after_drain"
	// ReasonSessionImport is a session import assembling its
	// background Host.
	ReasonSessionImport AssemblyReason = "session_import"
	// ReasonAutomation is a scheduled run assembling its background
	// Host.
	ReasonAutomation AssemblyReason = "automation"
	// ReasonConversation is a turn acquiring the Host of the workspace
	// that owns its conversation.
	ReasonConversation AssemblyReason = "conversation"
)

type assemblyReasonKey struct{}

// WithAssemblyReason tags ctx so the next assembly (and any
// invalidation on the way to it) reports reason instead of "unknown".
func WithAssemblyReason(
	ctx context.Context,
	reason AssemblyReason,
) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, assemblyReasonKey{}, reason)
}

// AssemblyReasonFrom returns the reason tagged on ctx, defaulting to
// ReasonUnknown. A nil context reports ReasonUnknown too.
func AssemblyReasonFrom(ctx context.Context) AssemblyReason {
	if ctx == nil {
		return ReasonUnknown
	}
	reason, _ := ctx.Value(assemblyReasonKey{}).(AssemblyReason)
	if reason == "" {
		return ReasonUnknown
	}
	return reason
}
