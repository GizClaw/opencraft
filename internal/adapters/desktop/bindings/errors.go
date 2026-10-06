// Package bindings contains the Wails-facing API objects for
// desktop. Each binding is a thin adapter over a core service; no
// binding owns domain state.
package bindings

import "fmt"

func errNotReady(domain string) error {
	return fmt.Errorf("%s: runtime is not ready", domain)
}

// errNoWorkspace is what a call that needs a workspace answers when
// neither the request nor the window names one. It is the domain's own
// refusal, not the pool's: the raw host sentinel ("host: no target to
// serve") is a programmer's contract, and a start that reached the pool
// unnamed would mint a conversation id for a workspace that cannot
// serve it before failing.
func errNoWorkspace(domain string) error {
	return fmt.Errorf("%s: open a workspace first", domain)
}
