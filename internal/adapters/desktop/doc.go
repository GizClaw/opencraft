// Package desktop is the shared desktop adapter tree. It hosts:
//   - core: the Wails-agnostic service composition root and domain services;
//   - bindings: DTO/binding objects shared by the v2 and v3 UI shells;
//   - mainthread: the darwin main-queue helper used while fyne systray remains;
//   - the root Shell service for the Wails v3 entry (build-tagged `wails3`).
//
// desktopv2 keeps only the Wails v2 UI shell during the migration and is
// removed once the v3 line replaces it. Dependency direction is the same as
// desktopv2: this package tree may import orchestration/capabilities/
// foundation but never the reverse.
package desktop
