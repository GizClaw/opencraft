package files

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
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

	ws := newTestWorkspace(t)
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
	text := out.Text()
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("tool result not valid JSON: %v\n%s", err, text[:200])
	}
	content, _ := envelope["content"].(string)
	if !strings.Contains(content, "diff --git") {
		t.Fatalf("unexpected content: %.60q", content)
	}

	// The envelope must survive the message round trip byte-identically.
	part := message.ToolResultPart{Result: message.ToolResult{Content: message.NewTextContent(text)}}
	b, err := json.Marshal(part)
	if err != nil {
		t.Fatal(err)
	}
	var back message.ToolResultPart
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Result.Content.Text() != text {
		t.Fatalf("content corrupted through message round trip:\n got %.80q\nwant %.80q",
			back.Result.Content.Text(), text)
	}
}
