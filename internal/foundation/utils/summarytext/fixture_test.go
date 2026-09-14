package summarytext

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

// compactCase is one fixture entry: a message list, its Go rendering,
// and its Go estimate. The graph's JS compaction node is checked
// against the same file (frontend/src/lib/compactMirror.test.ts), so a
// change to either implementation that is not mirrored fails a test
// instead of quietly shifting when compaction triggers.
type compactCase struct {
	Name string `json:"name"`
	// Messages is the wire form the board carries, which is what the
	// JS node reads.
	Messages []json.RawMessage `json:"messages"`
	// Rendered is RenderMessage per message, in order.
	Rendered []string `json:"rendered"`
	// Tokens is EstimateTokens over the whole list.
	Tokens int `json:"tokens"`
}

type compactFixture struct {
	Note string `json:"note"`
	// SummaryPrefix lets the frontend assert its own copy of the marker
	// (frontend/src/lib/compact.ts) against this one instead of trusting
	// two hand-kept duplicates.
	SummaryPrefix string        `json:"summaryPrefix"`
	Cases         []compactCase `json:"cases"`
}

// imageSource builds one URL-backed image source for the media case.
func imageSource(t *testing.T) media.ImageSource {
	t.Helper()
	src, err := media.NewImageURL(
		"https://example.com/shot.png", "image/png")
	if err != nil {
		t.Fatalf("image source: %v", err)
	}
	return src
}

// compactCaseMessages covers every branch of the renderer and the
// estimate: text only, CJK, mixed scripts, astral code points (JS
// counts two code units), tool calls and results alone and combined,
// an empty message, a summary marker, and a multi-message round.
func compactCaseMessages(t *testing.T) []struct {
	name string
	msgs []message.Message
} {
	t.Helper()
	text := func(role message.Role, s string) message.Message {
		return message.NewTextMessage(role, s)
	}
	call := func(id, name, args string) message.Message {
		return message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: id, Name: name, Arguments: json.RawMessage(args),
				}},
			}},
		}
	}
	result := func(callID, content string) message.Message {
		return message.Message{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: callID, Content: message.NewTextContent(content),
				}},
			}},
		}
	}
	withTextAndTools := func(text string) message.Message {
		return message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: text},
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "exec_command",
					Arguments: json.RawMessage(`{"cmd":"go test ./..."}`),
				}},
				message.ToolResultPart{Result: message.ToolResult{
					CallID:  "c1",
					Content: message.NewTextContent("ok\n"),
				}},
			}},
		}
	}
	return []struct {
		name string
		msgs []message.Message
	}{
		{"empty list", nil},
		{"text only", []message.Message{text(message.RoleUser, "hello there")}},
		{"cjk text", []message.Message{text(message.RoleUser, "请把这段代码重构一下")}},
		{"mixed scripts", []message.Message{
			text(message.RoleAssistant, "done — 已完成 3 files"),
		}},
		{"astral code point", []message.Message{
			text(message.RoleUser, "ship it 🚀"),
		}},
		{"tool call only", []message.Message{
			call("c1", "read_file", `{"path":"a.txt"}`),
		}},
		{"tool result only", []message.Message{result("c1", "file body")}},
		// The arguments arrive as raw JSON. Go renders the bytes it
		// holds; the JS node only ever sees the parsed value and
		// re-serializes it, so a non-canonical spelling is a place the
		// two can disagree.
		{"arguments with whitespace", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "exec_command",
					Arguments: json.RawMessage(
						"{\n  \"cmd\": \"go build ./...\",\n  \"cwd\": \"/w\"\n}"),
				}},
			}},
		}}},
		// Reasoning is not part of the prompt form in either
		// implementation: it is neither text nor tool activity.
		{"reasoning beside text", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ReasoningPart{Text: "thinking about it"},
				message.TextPart{Text: "the answer"},
			}},
		}}},
		// Several text parts concatenate without a separator.
		{"split text parts", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "first "},
				message.TextPart{Text: "second"},
			}},
		}}},
		// A result that carries media beside its text contributes the
		// text only.
		{"tool result with media", []message.Message{{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1",
					Content: message.Content{Parts: []message.Part{
						message.TextPart{Text: "ok"},
						message.ImagePart{Source: imageSource(t)},
					}},
				}},
			}},
		}}},
		{"text with tool activity", []message.Message{
			withTextAndTools("running the suite"),
		}},
		{"tool activity without text", []message.Message{
			withTextAndTools(""),
		}},
		// A message whose parts carry no text renders to nothing in
		// both implementations: the estimate still charges the
		// per-message overhead.
		{"media part only", []message.Message{{
			Role: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.ImagePart{Source: imageSource(t)},
			}},
		}}},
		{"summary marker", []message.Message{
			text(message.RoleUser, SummaryPrefix+"\nfolded older rounds"),
		}},
		{
			"one round with a tool call and its result",
			[]message.Message{
				text(message.RoleUser, "check the build"),
				call("c1", "exec_command", `{"cmd":"go build ./..."}`),
				result("c1", ""),
				text(message.RoleAssistant, "build is green"),
			},
		},
	}
}

// TestCompactFixtureMatchesRenderAndEstimate pins the fixture to the Go
// implementation. Run with UPDATE_GOLDEN=1 after an intentional change,
// then run the frontend test that reads the same file.
func TestCompactFixtureMatchesRenderAndEstimate(t *testing.T) {
	cases := compactCaseMessages(t)
	fixture := compactFixture{
		Note: "Generated by TestCompactFixtureMatchesRenderAndEstimate " +
			"(UPDATE_GOLDEN=1). The graph's JS compaction node is checked " +
			"against this file by frontend/src/lib/compactMirror.test.ts.",
		SummaryPrefix: SummaryPrefix,
		Cases:         make([]compactCase, 0, len(cases)),
	}
	for _, tc := range cases {
		entry := compactCase{
			Name:     tc.name,
			Messages: make([]json.RawMessage, 0, len(tc.msgs)),
			Rendered: make([]string, 0, len(tc.msgs)),
			Tokens:   EstimateTokens(tc.msgs),
		}
		for _, m := range tc.msgs {
			raw, err := json.Marshal(m)
			if err != nil {
				t.Fatalf("%s: marshal message: %v", tc.name, err)
			}
			entry.Messages = append(entry.Messages, raw)
			entry.Rendered = append(entry.Rendered, RenderMessage(m))
		}
		fixture.Cases = append(fixture.Cases, entry)
	}
	got, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	got = append(got, '\n')

	path := filepath.Join("testdata", "compact_cases.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", path, len(got))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("fixture is stale; rerun with UPDATE_GOLDEN=1:\n--- want\n%s\n--- got\n%s",
			want, got)
	}
}
