//go:build darwin

package desktop

/*
#include <stdbool.h>
*/
import "C"

// opencraftMainThreadTrampoline runs the function stashed by runOnMain.
// It lives in its own file because a preamble containing definitions is
// not allowed in a file that uses //export.
//
//export opencraftMainThreadTrampoline
func opencraftMainThreadTrampoline() {
	mainThreadMu.Lock()
	fn := mainThreadFn
	mainThreadFn = nil
	mainThreadMu.Unlock()
	if fn != nil {
		fn()
	}
}
