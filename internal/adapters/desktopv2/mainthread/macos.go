//go:build darwin

package mainthread

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

extern void opencraftMainThreadTrampoline(void);

static void dispatchToMain(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		opencraftMainThreadTrampoline();
	});
}
*/
import "C"

import "sync"

var (
	mu sync.Mutex
	fn func()
)

//export opencraftMainThreadTrampoline
func opencraftMainThreadTrampoline() {
	mu.Lock()
	next := fn
	fn = nil
	mu.Unlock()
	if next != nil {
		next()
	}
}

// Run schedules fn on the main queue so systray can create the status
// item on the native application main thread.
func Run(nextFn func()) {
	mu.Lock()
	fn = nextFn
	mu.Unlock()
	C.dispatchToMain()
}
