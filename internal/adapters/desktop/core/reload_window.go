package core

// A window's memory boundary is the document swap: bounding what the DOM
// holds stops the next allocation, it does not hand back what WebKit
// already holds, and only a fresh document gives the process its
// high-water mark back. ReloadWindow is that decision. It lives here, next
// to the services whose state it consults, because the two halves of it
// are the same question asked twice: is anything still writing into this
// page (BusyWorkspaces), and is there a page to replace (Shell.reloadMain).
// An adapter that asked only the second question would be a loaded gun.

// ReloadOutcome is what a memory-boundary reload did, and why it did not.
type ReloadOutcome struct {
	// Reloaded reports that the window was told to drop its document.
	Reloaded bool
	// Reason names the refusal: empty when Reloaded, otherwise one of
	// ReloadBusy and ReloadNoWindow.
	Reason string
	// Busy lists the workspaces whose live runs held the reload back, in
	// the pool's order. Only set when Reason is ReloadBusy.
	Busy []string
}

const (
	// ReloadBusy means a run is live somewhere in the process. Its text
	// arrives as deltas through this window's one event channel, and a
	// reload lands the new page after the deltas it never saw: a turn
	// that stays empty until it ends. The scope is the window, not the
	// repository - the channel is process-wide, so a run in a workspace
	// the reader is not looking at is just as fatal to it.
	ReloadBusy = "busy"
	// ReloadNoWindow means there was no main window to reload: headless
	// has none, and before the entry point attaches one neither has the
	// desktop shell.
	ReloadNoWindow = "no-window"
)

// ReloadWindow asks the main window to drop its document and load the page
// again, unless a live run would lose text to the swap.
//
// The busy check and the reload are not atomic, and cannot be: the pool
// guards the workspace table, the run table is per Host, and a run starts
// without the frontend's participation (an automation occurrence is the
// ordinary case). A run that lands in that window is the residual race the
// caller accepts - which is why the frontend treats a reload as
// best-effort and repeats it on the next switch instead of retrying here.
func (c *Core) ReloadWindow() ReloadOutcome {
	if busy := c.BusyWorkspaces(); len(busy) > 0 {
		return ReloadOutcome{Reason: ReloadBusy, Busy: busy}
	}
	if c.reloadWindow == nil || !c.reloadWindow() {
		return ReloadOutcome{Reason: ReloadNoWindow}
	}
	return ReloadOutcome{Reloaded: true}
}
