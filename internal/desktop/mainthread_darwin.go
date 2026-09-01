//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

extern void opencraftMainThreadTrampoline(void);

// opencraftDispatchToMain runs the registered Go trampoline on the main
// queue. The systray status item must be created there; Wails invokes
// OnStartup from a background goroutine, so a direct call would leave
// the menu bar empty.
static void opencraftDispatchToMain(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		opencraftMainThreadTrampoline();
	});
}
*/
import "C"

import "sync"

var (
	mainThreadMu sync.Mutex
	mainThreadFn func()
)

// runOnMain schedules fn on the main thread. cgo cannot pass Go
// closures to C, so the function is stashed in mainThreadFn and picked
// up by the exported trampoline (mainthread_trampoline_darwin.go).
func runOnMain(fn func()) {
	mainThreadMu.Lock()
	mainThreadFn = fn
	mainThreadMu.Unlock()
	C.opencraftDispatchToMain()
}
