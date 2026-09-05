//go:build yoloonly

package profile

import "testing"

func TestYoloOnlyBuildProfile(t *testing.T) {
	if !YoloOnly() {
		t.Fatal("yoloonly build must report the profile")
	}
}
