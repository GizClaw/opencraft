package websearch

import (
	"encoding/json"
	"testing"
)

// mcpTextEnvelope renders the JSON-RPC response both vendors send for
// a successful tools/call.
func mcpTextEnvelope(t *testing.T, text string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result": map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestParseMCPResponseDirectJSON(t *testing.T) {
	text, err := parseMCPResponse([]byte(mcpTextEnvelope(t, "hello")))
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello" {
		t.Fatalf("text = %q", text)
	}
}

func TestParseMCPResponseSSE(t *testing.T) {
	body := "event: message\ndata: " + mcpTextEnvelope(t, "streamed") + "\n\n"
	text, err := parseMCPResponse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if text != "streamed" {
		t.Fatalf("text = %q", text)
	}
}

func TestParseMCPResponseProtocolError(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"bad args"}}`
	if _, err := parseMCPResponse([]byte(body)); err == nil {
		t.Fatal("protocol error must fail")
	}
}

func TestParseMCPResponseToolError(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":{"isError":true,` +
		`"content":[{"type":"text","text":"upstream failed"}]}}`
	if _, err := parseMCPResponse([]byte(body)); err == nil {
		t.Fatal("tool error must fail")
	}
}

func TestParseMCPResponseUnexpectedShape(t *testing.T) {
	if _, err := parseMCPResponse([]byte("<html>nope</html>")); err == nil {
		t.Fatal("non-JSON body must fail rather than read as no results")
	}
}
