// Package desktop is the Wails v3 desktop adapter tree. It hosts:
//   - core: the service composition root and domain services;
//   - bindings: the DTO/binding objects registered as Wails v3 services;
//   - the root Desktop composition and Shell service used by the entry point.
//
// Dependency direction: this package tree may import orchestration/
// capabilities/foundation but never the reverse.
package desktop
