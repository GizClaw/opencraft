// Package bindings adapts the desktop core services for the Wails UI
// shells (v2 today, v3 during the migration). It holds no domain state:
// every binding is a thin DTO adapter over core.Core services, and domain
// logic lives in orchestration/host or the capabilities below it.
//
// Direct imports of capability packages in this directory must stay
// type/value-only (DTO shapes, domain constants, validation and
// pure helper functions such as plugins.ValidateSecretRef). Store,
// manager or engine state must never be constructed or mutated here:
// reach it through core services (b.core.Runtime, b.core.Plugin,
// b.core.Shell, ...) or through the Host surface they expose.
//
// scripts/check-boundaries.sh enforces the package-level part of this
// contract in CI; the "no direct state" part is a review rule kept in
// mind when adding bindings.
package bindings
