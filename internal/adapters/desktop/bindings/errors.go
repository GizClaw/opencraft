// Package bindings contains the Wails-facing API objects for
// desktop. Each binding is a thin adapter over a core service; no
// binding owns domain state.
package bindings

import "fmt"

func errNotReady(domain string) error {
	return fmt.Errorf("%s: runtime is not ready", domain)
}
