//go:build !yoloonly

package profile

import "testing"

func TestRegularBuildKeepsAllModes(t *testing.T) {
	if YoloOnly() {
		t.Fatal("regular build must not report the yoloonly profile")
	}
}
