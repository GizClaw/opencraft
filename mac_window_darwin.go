//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation -framework Cocoa -framework WebKit
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

// The window runs MacTitleBarHiddenInset (a full-size content view), which
// parks the standard window buttons 26pt below and right of the window's top
// left corner. The chat header strip that shares that corner is 44pt tall, so
// its centre - where the title and the strip's own controls line up - sits at
// 22pt from the top. Wails v3 exposes no way to move the buttons, so they are
// moved here instead of letting the strip grow around them.
static const CGFloat ocTitlebarCentreY = 22.0;

// ocNudgeTrafficLights centres every standard window button 22pt below the
// window's top edge. Skipped in fullscreen, where macOS owns the placement and
// keeps the buttons hidden until the pointer reaches the top edge.
static void ocNudgeTrafficLights(NSWindow *window) {
	if (window == nil) {
		return;
	}
	if (([window styleMask] & NSWindowStyleMaskFullScreen) != 0) {
		return;
	}
	CGFloat windowHeight = [window frame].size.height;
	NSButton *buttons[] = {
		[window standardWindowButton:NSWindowCloseButton],
		[window standardWindowButton:NSWindowMiniaturizeButton],
		[window standardWindowButton:NSWindowZoomButton],
	};
	for (int i = 0; i < 3; i++) {
		NSButton *button = buttons[i];
		if (button == nil) {
			continue;
		}
		NSRect frame = [button convertRect:[button bounds] toView:nil];
		frame.origin.y = windowHeight - ocTitlebarCentreY - frame.size.height / 2.0;
		[button setFrame:[[button superview] convertRect:frame fromView:nil]];
	}
}

// ocObserveWindowLayout re-applies the nudge after every frame change AppKit
// makes: a live resize, the zoom button, leaving fullscreen. Each of those
// re-lays out the titlebar and drops the buttons back at AppKit's own 26pt, so
// the fix is only stable while something repeats it. The second pass on the
// next runloop turn covers a layout that was already queued behind the
// notification.
static void ocObserveWindowLayout(NSWindow *window) {
	static char observedKey;
	if (objc_getAssociatedObject(window, &observedKey) != nil) {
		return;
	}
	objc_setAssociatedObject(window, &observedKey, @YES, OBJC_ASSOCIATION_RETAIN);
	NSNotificationCenter *centre = [NSNotificationCenter defaultCenter];
	void (^reapply)(NSNotification *) = ^(NSNotification *note) {
		NSWindow *changed = [note object];
		ocNudgeTrafficLights(changed);
		dispatch_async(dispatch_get_main_queue(), ^{
			ocNudgeTrafficLights(changed);
		});
	};
	NSArray<NSString *> *names = @[
		NSWindowDidResizeNotification,
		NSWindowDidEndLiveResizeNotification,
		NSWindowDidExitFullScreenNotification,
	];
	for (NSString *name in names) {
		[centre addObserverForName:name object:window
			queue:[NSOperationQueue mainQueue] usingBlock:reapply];
	}
}

// ocResolveMainWindow is the fallback for a call that arrives before Wails has
// handed out the native window: it picks the largest titled, non-panel window.
// Borderless surfaces (the desktop pet) and panels (dialogs) are skipped, and
// a window without a titlebar has no standard buttons to move anyway.
static NSWindow *ocResolveMainWindow(void) {
	NSWindow *candidate = nil;
	CGFloat best = 0;
	for (NSWindow *window in [[NSApplication sharedApplication] windows]) {
		if (([window styleMask] & NSWindowStyleMaskTitled) == 0) {
			continue;
		}
		if ([window isKindOfClass:[NSPanel class]]) {
			continue;
		}
		CGFloat area = [window frame].size.width * [window frame].size.height;
		if (candidate == nil || area > best) {
			candidate = window;
			best = area;
		}
	}
	return candidate;
}

// disableScrollElasticity turns off the rubber-band bounce on every scroll
// view inside the webview so two-finger gestures on the trackpad never shake
// the whole UI. The full content tree is walked because the WKWebView nesting
// changes across OS and Wails runtime versions.
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

// ocApplyWindowStyle is the entry point from Go. It takes the main window's
// NSWindow (Wails' WebviewWindow.NativeWindow) and is safe to call repeatedly:
// the button move is idempotent and the observers are installed once per
// window.
static void ocApplyWindowStyle(void *handle) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *window = (NSWindow *)handle;
		if (window == nil) {
			window = ocResolveMainWindow();
		}
		if (window == nil) {
			return;
		}
		ocNudgeTrafficLights(window);
		ocObserveWindowLayout(window);
		disableScrollElasticity([window contentView]);
	});
}
*/
import "C"

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// applyOpenCraftWindowStyle is the darwin implementation of the window polish:
// traffic lights centred on the chat header, no rubber-band scrolling.
func applyOpenCraftWindowStyle(w *application.WebviewWindow) {
	if w == nil {
		C.ocApplyWindowStyle(nil)
		return
	}
	C.ocApplyWindowStyle(w.NativeWindow())
}

// registerOpenCraftWindowStyleRefresh applies the window polish after the
// first WebKit page load. Wails v3 exposes no equivalent of the v2 startup
// timing for this, so the darwin-specific "navigation finished" event is
// used; other platforms keep native window chrome and need nothing.
func registerOpenCraftWindowStyleRefresh(w *application.WebviewWindow) {
	w.OnWindowEvent(events.Mac.WebViewDidFinishNavigation,
		func(*application.WindowEvent) {
			applyOpenCraftWindowStyle(w)
		})
}
