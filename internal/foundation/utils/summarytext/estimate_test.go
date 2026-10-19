package summarytext

import (
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

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

// imageBytes builds one inline image source of n bytes. The estimate
// charges the encoded length, so the fixture needs a payload whose size is
// pinned rather than a real screenshot.
func imageBytes(t *testing.T, n int) media.ImageSource {
	t.Helper()
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(i % 251)
	}
	src, err := media.NewImageBytes(data, "image/png")
	if err != nil {
		t.Fatalf("image bytes: %v", err)
	}
	return src
}

// The estimate is what decides when a turn folds its older rounds, so its
// exact numbers are a contract: a change here moves the threshold every
// deployment budgets against. These cases cover every branch of the
// renderer and the estimate — text only, CJK, mixed scripts, astral code
// points (two UTF-16 units), tool calls and results alone and combined,
// an empty message, media (charged by payload, not by words), and a
// summary marker.
//
// The numbers used to be pinned against the graph node's JS mirror, which
// read the same table through testdata/compact_cases.json. The node now
// calls this estimate (capabilities/compaction), so the table only has to
// hold Go to its own history.
func TestEstimateTokens(t *testing.T) {
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
	withTextAndTools := func(body string) message.Message {
		return message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: body},
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
	for _, tc := range []struct {
		name string
		msgs []message.Message
		want int
	}{
		{"empty list", nil, 0},
		{"text only", []message.Message{text(message.RoleUser, "hello there")}, 11},
		{"cjk text", []message.Message{text(message.RoleUser, "请把这段代码重构一下")}, 18},
		{"mixed scripts", []message.Message{
			text(message.RoleAssistant, "done — 已完成 3 files"),
		}, 15},
		{"astral code point", []message.Message{
			text(message.RoleUser, "ship it 🚀"),
		}, 11},
		{"tool call only", []message.Message{
			call("c1", "read_file", `{"path":"a.txt"}`),
		}, 18},
		{"tool result only", []message.Message{result("c1", "file body")}, 14},
		// The arguments arrive as raw JSON; the renderer compacts them, so
		// a pretty-printed call and a tight one cost the same.
		{"arguments with whitespace", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{
					ID: "c1", Name: "exec_command",
					Arguments: json.RawMessage(
						"{\n  \"cmd\": \"go build ./...\",\n  \"cwd\": \"/w\"\n}"),
				}},
			}},
		}}, 23},
		// Reasoning is neither text nor tool activity: it is not part of
		// the prompt form.
		{"reasoning beside text", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ReasoningPart{Text: "thinking about it"},
				message.TextPart{Text: "the answer"},
			}},
		}}, 11},
		// Several text parts concatenate without a separator.
		{"split text parts", []message.Message{{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.TextPart{Text: "first "},
				message.TextPart{Text: "second"},
			}},
		}}, 11},
		// A result that carries media beside its text contributes the text
		// to the rendering and the payload to the estimate; a URL-backed
		// image pays the floor because the estimate cannot size the pixels.
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
		}}, 1036},
		// An inline image is charged by its encoded length: the URL form
		// above pays only the floor, a real payload pays more.
		{"inline image", []message.Message{{
			Role: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.ImagePart{Source: imageBytes(t, 4096)},
			}},
		}}, 1032},
		{"tool result with inline image", []message.Message{{
			Role: message.RoleTool,
			Content: message.Content{Parts: []message.Part{
				message.ToolResultPart{Result: message.ToolResult{
					CallID: "c1",
					Content: message.Content{Parts: []message.Part{
						message.TextPart{Text: "view_image: shot.png"},
						message.ImagePart{Source: imageBytes(t, 40960)},
					}},
				}},
			}},
		}}, 3430},
		{"text with tool activity", []message.Message{
			withTextAndTools("running the suite"),
		}, 29},
		{"tool activity without text", []message.Message{
			withTextAndTools(""),
		}, 24},
		// A message whose parts carry no text renders to nothing; the
		// estimate still charges the per-message overhead and the image.
		{"media part only", []message.Message{{
			Role: message.RoleUser,
			Content: message.Content{Parts: []message.Part{
				message.ImagePart{Source: imageSource(t)},
			}},
		}}, 1032},
		{"summary marker", []message.Message{
			text(message.RoleUser, SummaryPrefix+"\nfolded older rounds"),
		}, 38},
		{
			"one round with a tool call and its result",
			[]message.Message{
				text(message.RoleUser, "check the build"),
				call("c1", "exec_command", `{"cmd":"go build ./..."}`),
				result("c1", ""),
				text(message.RoleAssistant, "build is green"),
			},
			56,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := EstimateTokens(tc.msgs); got != tc.want {
				t.Fatalf("EstimateTokens = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestEstimateTextCountsUTF16Units pins the unit itself: an astral code
// point is two UTF-16 code units in one rune, so a text of emoji must
// cost what the same number of code units costs anywhere else.
func TestEstimateTextCountsUTF16Units(t *testing.T) {
	for _, tc := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"abcd", 1},
		{"abcde", 2},
		{"中文", 2},
		// Two runes, four UTF-16 units.
		{"🚀🚀", 1},
		{"a🚀", 1},
	} {
		if got := EstimateText(tc.text); got != tc.want {
			t.Fatalf("EstimateText(%q) = %d, want %d", tc.text, got, tc.want)
		}
	}
}
