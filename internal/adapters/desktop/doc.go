// Package desktop is the Wails v3 desktop adapter. During the migration it
// deliberately carries no domain state yet: it only proves the new shell
// lifecycle (application/window/service/event/tray) inside the same repository
// as the still-active desktopv2 adapter. Files in this package are build-tagged
// `wails3` except doc.go, so the default `go build ./...` keeps compiling the
// v2 line; desktopv2 is removed once the domain migration completes.
//
// Dependency direction is the same as desktopv2: this package may import
// orchestration/capabilities/foundation but never the reverse.
package desktop
