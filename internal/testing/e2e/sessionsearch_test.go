package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/headless"
	"github.com/GizClaw/opencraft/internal/capabilities/rollout"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestHeadlessSessionSearchRecallsEarlierSession drives the cross-session
// recall path end to end: one headless session writes a fact down, a
// second (same workspace, same state root) has to find it again through
// the deferred session_search tool — discovered via tool_search, then
// answered from the archived index of the first session.
func TestHeadlessSessionSearchRecallsEarlierSession(t *testing.T) {
	const token = "zebra42quokka"
	provider := fakeprovider.New(t,
		// Session one: the fact is only ever written here.
		fakeprovider.Reply{
			Text: "Noted: the deployment passphrase is " + token + ".",
		},
		// Session two: discover the recall tool, call it, answer.
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name: "tool_search",
			Arguments: `{"query":"search past conversations earlier ` +
				`sessions history recall"}`,
		}}},
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "session_search",
			Arguments: `{"query":"` + token + `"}`,
		}}},
		fakeprovider.Reply{Text: "Found it in an earlier session."},
	)
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	var out bytes.Buffer
	first, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "Remember the deployment passphrase.",
		Quiet:     true,
	})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if first.Status != "completed" {
		t.Fatalf("first run status = %q", first.Status)
	}

	out.Reset()
	second, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "What is the deployment passphrase? Look it up if it is not in this conversation.",
		Out:       &out,
		Quiet:     true,
	})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.Status != "completed" || second.ExitCode != 0 {
		t.Fatalf("second run = %+v", second)
	}
	if second.ConversationID == first.ConversationID {
		t.Fatalf("both runs used session %s; the recall is not cross-session",
			first.ConversationID)
	}

	var (
		sawCompleted bool
		sawSearch    bool
		callID       string
	)
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var ev rollout.Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("invalid JSONL event %q: %v", line, err)
		}
		switch ev.Type {
		case rollout.TypeTurnCompleted:
			sawCompleted = ev.Status == "completed"
		case rollout.TypeItemToolCall:
			if ev.Tool != "session_search" {
				continue
			}
			sawSearch = true
			callID = ev.CallID
			if !strings.Contains(string(ev.Arguments), token) {
				t.Fatalf("session_search arguments = %s", ev.Arguments)
			}
		case rollout.TypeItemToolResult:
			if !sawSearch || ev.CallID != callID {
				continue
			}
			// The result must quote the first session and its snippet.
			if !strings.Contains(ev.Content, token) ||
				!strings.Contains(ev.Content, "["+token+"]") ||
				!strings.Contains(ev.Content, first.ConversationID) {
				t.Fatalf("session_search result did not recall the first session: %s",
					ev.Content)
			}
			if ev.IsError {
				t.Fatalf("session_search failed: %s", ev.Content)
			}
		}
	}
	if !sawCompleted {
		t.Fatalf("second run never reported a completed turn:\n%s", out.String())
	}
	if !sawSearch {
		t.Fatalf("second run never called session_search:\n%s", out.String())
	}
}
