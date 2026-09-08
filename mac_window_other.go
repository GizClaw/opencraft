//go:build !darwin

package main

import "github.com/wailsapp/wails/v3/pkg/application"

// applyOpenCraftWindowStyle is a no-op outside macOS: the traffic-light
// alignment and WKWebView scroll elasticity only exist on the mac window.
func applyOpenCraftWindowStyle() {}

// registerOpenCraftWindowStyleRefresh is a no-op outside macOS: the window
// has no traffic lights or rubber-band scrolling to polish after page load.
func registerOpenCraftWindowStyleRefresh(_ *application.WebviewWindow) {}
