package pet

import (
	"bytes"
	"testing"
)

// TestBuiltinAssistantPackAsset verifies the embedded character asset
// is present and carries the RIVE file fingerprint.
func TestBuiltinAssistantPackAsset(t *testing.T) {
	asset := BuiltinAssistantPackAsset()
	if len(asset) == 0 {
		t.Fatal("builtin assistant .riv is empty")
	}
	if !bytes.HasPrefix(asset, []byte("RIVE")) {
		t.Fatalf("builtin assistant .riv has bad fingerprint: %q", asset[:4])
	}
}
