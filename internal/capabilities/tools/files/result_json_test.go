package files

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/workspace"
)

// TestReadFileResultJSONRoundTrip guards the tool result serialization:
// the read_file result and its embedded content must stay valid JSON
// through the message marshal/unmarshal path. A historical archive bug
// stored a raw newline inside the result JSON, which broke resume
// rendering; this test pins the escaping so it cannot regress.
func TestReadFileResultJSONRoundTrip(t *testing.T) {
	diff := "diff --git a/x.ts b/x.ts\n" +
		"index cd3b385..d838463 100644\n" +
		"--- a/x.ts\n" +
		"+++ b/x.ts\n" +
		"@@ -47,6 +47,7 @@ import type {\n" +
		"+    const pin = true;\n" +
		"   TurnDoc,\n" +
		" }\n" +
		"\"\n" + // literal quote inside the content
		"trailing\n"

	ws, err := workspace.NewLocalWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.Write(context.Background(), "x.diff", []byte(diff)); err != nil {
		t.Fatal(err)
	}
	tool := &readFileTool{ws: ws}
	out, err := tool.Execute(context.Background(), `{"file_path":"x.diff"}`)
	if err != nil {
		t.Fatal(err)
	}

	// The tool result must be valid JSON.
	var envelope map[string]any
	if err := json.Unmarshal([]byte(out), &envelope); err != nil {
		t.Fatalf("tool result not valid JSON: %v\n%s", err, out[:200])
	}
	content, _ := envelope["content"].(string)
	if !strings.Contains(content, "diff --git") {
		t.Fatalf("unexpected content: %.60q", content)
	}

	// The envelope must survive the message round trip byte-identically.
	part := message.ToolResultPart{Result: message.ToolResult{Content: out}}
	b, err := json.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Result struct {
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Result.Content != out {
		t.Fatalf("content corrupted through message round trip:\n got %.80q\nwant %.80q",
			back.Result.Content, out)
	}
}
