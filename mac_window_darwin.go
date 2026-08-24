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
	// The title bar uses flipped coordinates (origin at the top, y
	// grows downward). Measured default: origin.y = 33 for a 14pt
	// button, i.e. the centre sits at 40pt, while the 44px chat header
	// title is centred at 22pt. Pin the buttons at origin.y = 15 so
	// their centre (22pt) matches the title.
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
		frame.origin.y = 15.0;
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
