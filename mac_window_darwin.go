//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// applyOpenCraftWindowStyle nudges the traffic lights vertically so
// they line up with the chat header title. The system title bar stays
// hidden-but-native (TitleBarHiddenInset), so corners, shadow, and the
// buttons themselves remain standard macOS chrome.
static void applyOpenCraftWindowStyleInner(void) {
	NSWindow *w = [[NSApplication sharedApplication] windows].firstObject;
	if (w == nil) {
		return;
	}
	// Traffic lights sit in the title bar, whose coordinates are
	// flipped relative to the content: increase the origin to raise
	// them. A small positive offset makes their vertical centre match
	// the 44px chat header title.
	const CGFloat dy = 3.0;
	NSButton *buttons[] = {
		[w standardWindowButton:NSWindowCloseButton],
		[w standardWindowButton:NSWindowMiniaturizeButton],
		[w standardWindowButton:NSWindowZoomButton],
	};
	for (int i = 0; i < 3; i++) {
		NSButton *b = buttons[i];
		if (b == nil) {
			continue;
		}
		NSRect frame = [b frame];
		frame.origin.y += dy;
		[b setFrame:frame];
	}
}

static void applyOpenCraftWindowStyle(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		applyOpenCraftWindowStyleInner();
	});
}
*/
import "C"

// applyOpenCraftWindowStyle is the darwin implementation of the window
// polish applied after startup.
func applyOpenCraftWindowStyle() {
	C.applyOpenCraftWindowStyle()
}
