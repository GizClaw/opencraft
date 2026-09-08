//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>

// applyOpenCraftWindowStyle nudges the traffic lights vertically so they line
// up with the chat header title and disables WKWebView rubber-band scrolling,
// matching the pre-v3 OpenCraft window polish. Wails v3 does not expose these
// two behaviours as window options yet.
static void disableScrollElasticity(NSView *view) {
	if (view == nil) {
		return;
	}
	if ([view isKindOfClass:[NSScrollView class]]) {
		NSScrollView *sv = (NSScrollView *)view;
		[sv setVerticalScrollElasticity:NSScrollElasticityNone];
		[sv setHorizontalScrollElasticity:NSScrollElasticityNone];
	}
	for (NSView *sub in [view subviews]) {
		disableScrollElasticity(sub);
	}
}

static void applyOpenCraftWindowStyleInner(void) {
	NSWindow *w = [[NSApplication sharedApplication] windows].firstObject;
	if (w == nil) {
		return;
	}
	// Buttons sit centred 26pt from the top in a 900pt window; move each so
	// its centre lands 22pt from the top (chat header centre).
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
		CGFloat winHeight = [w frame].size.height;
		NSRect winFrame = [b convertRect:[b bounds] toView:nil];
		winFrame.origin.y = winHeight - 22 - winFrame.size.height / 2.0;
		NSRect superFrame = [[b superview] convertRect:winFrame fromView:nil];
		[b setFrame:superFrame];
	}
	// Disable the rubber-band bounce on every scroll view inside the
	// webview so two-finger gestures on the trackpad never shake the whole
	// UI. Walk the full content tree; the WKWebView nesting differs between
	// Wails v2 and v3.
	disableScrollElasticity([w contentView]);
}

static void applyOpenCraftWindowStyle(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		applyOpenCraftWindowStyleInner();
	});
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// applyOpenCraftWindowStyle is the darwin implementation of the window polish
// applied once the main window has finished loading its first page.
func applyOpenCraftWindowStyle() {
	C.applyOpenCraftWindowStyle()
}

// registerOpenCraftWindowStyleRefresh applies the window polish after the
// first WebKit page load. Wails v3 exposes no equivalent of the v2 startup
// timing for this, so the darwin-specific "navigation finished" event is
// used; other platforms keep native window chrome and need nothing.
func registerOpenCraftWindowStyleRefresh(w *application.WebviewWindow) {
	w.OnWindowEvent(events.Mac.WebViewDidFinishNavigation,
		func(*application.WindowEvent) {
			applyOpenCraftWindowStyle()
		})
}
