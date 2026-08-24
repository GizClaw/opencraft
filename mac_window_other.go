//go:build !darwin

package main

// applyOpenCraftWindowStyle is a no-op on platforms without the native
// macOS window chrome; they keep the plain frameless rectangle.
func applyOpenCraftWindowStyle() {}
