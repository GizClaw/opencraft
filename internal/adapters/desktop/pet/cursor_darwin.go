//go:build darwin && cgo

package pet

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit
#import <AppKit/AppKit.h>
#import <dispatch/dispatch.h>
#import <math.h>

typedef struct {
	int x;
	int y;
	int ok;
} oc_cursor_point;

static void oc_cursor_read(void *context) {
	oc_cursor_point *point = (oc_cursor_point *)context;
	NSScreen *primary = [NSScreen screens].firstObject;
	if (primary == nil) {
		return;
	}
	CGFloat height = primary.frame.size.height;
	NSPoint cursor = [NSEvent mouseLocation];
	point->x = (int)lround(cursor.x);
	// AppKit's global origin is the bottom-left of the primary screen;
	// window coordinates grow downwards from its top-left, which is the
	// space windowGetPosition reports (webview_window_darwin.go).
	point->y = (int)lround(height - cursor.y);
	point->ok = 1;
}

// oc_cursor_position hops to the main thread: NSEvent is not safe to
// call from the window loop's goroutine. Reading the pointer needs no
// permissions.
static int oc_cursor_position(int *x, int *y) {
	oc_cursor_point point = {0, 0, 0};
	if ([NSThread isMainThread]) {
		oc_cursor_read(&point);
	} else {
		dispatch_sync_f(dispatch_get_main_queue(), &point, oc_cursor_read);
	}
	*x = point.x;
	*y = point.y;
	return point.ok;
}
*/
import "C"

func cursorPosition(_ uintptr) (x, y int, ok bool) {
	var cx, cy C.int
	if C.oc_cursor_position(&cx, &cy) == 0 {
		return 0, 0, false
	}
	return int(cx), int(cy), true
}
