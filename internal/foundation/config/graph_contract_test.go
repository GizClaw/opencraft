package config

import (
	"strings"
	"testing"
)

// TestCompactArchiveChannelMatchesGraphAsset pins the board channel the
// compact node moves folded conversation onto. The graph asset is a JS
// script that cannot import the Go constant, so the literal is checked
// here: a rename on either side would silently split the model view
// from the durable turn archive.
func TestCompactArchiveChannelMatchesGraphAsset(t *testing.T) {
	data, err := FS().ReadFile("assets/graphs/nodes/compact.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"`+CompactArchiveChannel+`"`) {
		t.Fatalf("compact.js does not reference %q; the memory hooks read that channel",
			CompactArchiveChannel)
	}
}
