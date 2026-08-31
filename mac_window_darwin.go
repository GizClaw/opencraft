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
// disableScrollElasticity walks a view subtree and turns off rubber-band
// scrolling on every scroll view it finds. Wails' webview does not
// expose scrollView through KVC (that crashed with
// NSUnknownKeyException), so locate the scroll views structurally.
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
	// Measured: the buttons sit at window y=867 in a 900pt window, i.e.
	// centred 26pt from the top, while the 44px chat header title is
	// centred at 22pt. Work in window coordinates (bottom-left origin):
	// move each button up so its centre lands at 22pt from the top.
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
	// Disable the rubber-band bounce on the webview's scroll view so
	// two-finger gestures on the trackpad never shake the whole UI.
	for (NSView *subview in [[w contentView] subviews]) {
		if ([subview isKindOfClass:[WKWebView class]]) {
			disableScrollElasticity(subview);
		}
	}
}

static void applyOpenCraftWindowStyle(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		applyOpenCraftWindowStyleInner();
	});
}

// reopenMainWindowIfHidden is invoked on every application activation
// (Dock click, Cmd+Tab, launch). Closing the window hides the whole app
// ([NSApp hide]) per the default macOS scheme; without this hook,
// clicking the Dock icon would activate the app but leave every window
// hidden. Only acts when the main window is actually invisible.
static void reopenMainWindowIfHidden(void) {
	NSApplication *app = [NSApplication sharedApplication];
	NSWindow *w = [app mainWindow];
	if (w == nil) {
		w = [app windows].firstObject;
	}
	if (w != nil && ![w isVisible]) {
		[w makeKeyAndOrderFront:nil];
		[app activateIgnoringOtherApps:YES];
	}
}

static void installOpenCraftReopenHandlerInner(void) {
	[[NSNotificationCenter defaultCenter]
		addObserverForName:NSApplicationDidBecomeActiveNotification
		object:nil
		queue:[NSOperationQueue mainQueue]
		usingBlock:^(NSNotification *note) {
			reopenMainWindowIfHidden();
		}];
}

static void installOpenCraftReopenHandler(void) {
	dispatch_async(dispatch_get_main_queue(), ^{
		installOpenCraftReopenHandlerInner();
	});
}
*/
import "C"

// applyOpenCraftWindowStyle is the darwin implementation of the window
// polish applied after startup.
func applyOpenCraftWindowStyle() {
	C.applyOpenCraftWindowStyle()
}

// installOpenCraftReopenHandler is the darwin implementation of the
// dock-click reopen hook: Wails v2 has no applicationShouldHandleReopen
// delegate hook, so we observe NSApplicationDidBecomeActiveNotification
// and bring the hidden main window back when the user activates the app.
func installOpenCraftReopenHandler() {
	C.installOpenCraftReopenHandler()
}
