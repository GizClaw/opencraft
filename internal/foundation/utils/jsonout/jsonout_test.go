package jsonout

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMarshalLeavesTheTextAlone: the escape set is JSON syntax only. The
// HTML set is what encoding/json adds for embedding a result in a page,
// and it is exactly the text a model copies out of a tool result.
func TestMarshalLeavesTheTextAlone(t *testing.T) {
	text := "if a && b {\n\tif c < d && e > f {\n\t}\n}\n"
	out, err := Marshal(map[string]any{"content": text})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	got := string(out)
	for _, unwanted := range []string{`\u0026`, `\u003c`, `\u003e`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("encoded result carries %s: %s", unwanted, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("result ends with the encoder's newline: %q", got)
	}
	// What the encoder does keep is the JSON syntax: the string still has
	// to decode to the same text.
	var back struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back.Content != text {
		t.Errorf("round trip = %q, want %q", back.Content, text)
	}
}

// TestMarshalStaysValidForTheQuoteAndBackslash: dropping the HTML set
// must not drop the escapes JSON requires.
func TestMarshalStaysValidForTheQuoteAndBackslash(t *testing.T) {
	out, err := Marshal("say \"hi\" \\ and \x01")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back string
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("decode %s: %v", out, err)
	}
	if back != "say \"hi\" \\ and \x01" {
		t.Errorf("round trip = %q", back)
	}
}
