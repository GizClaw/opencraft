//go:build !darwin

package main

// applyOpenCraftWindowStyle is a no-op on platforms without the native
// macOS window chrome; they keep the plain frameless rectangle.
func applyOpenCraftWindowStyle() {}

// installOpenCraftReopenHandler is a no-op on platforms without a Dock:
// Windows and Linux restore the window from the tray menu or the
// second-instance handler instead.
func installOpenCraftReopenHandler() {}

// installOpenCraftTerminateHandler is a no-op on platforms without a
// macOS application menu / Dock.
func installOpenCraftTerminateHandler() {}

// macConsumeTerminateRequest is always false off macOS.
func macConsumeTerminateRequest() bool { return false }
