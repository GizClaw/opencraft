//go:build yoloonly

package profile

// YoloOnly reports whether this binary was built with -tags yoloonly.
func YoloOnly() bool { return true }
