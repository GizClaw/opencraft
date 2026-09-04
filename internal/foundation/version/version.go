// Package version owns the single release version variable. It lives
// in foundation so both capabilities (telemetry) and adapters can
// depend on it without importing each other.
package version

// ServiceVersion identifies opencraft in exported telemetry
// (service.version resource attribute) and in the UI. Release builds
// override it via
// `-ldflags "-X github.com/GizClaw/opencraft/internal/foundation/version.ServiceVersion=v..."`.
var ServiceVersion = "0.1.0"
