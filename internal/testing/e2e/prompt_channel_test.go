package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/headless"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestBasePromptReachesProviderAsFirstSystemMessage runs the real
// assistant graph (world.js -> compact -> llm) against a fake provider
// and asserts the model-visible channel shape: the base identity is
// the first system message, it appears exactly once, and the per-turn
// world state (environment) follows it. This guards the "base lives in
// world sections" refactor against regressions where the system prompt
// silently drops out of the channel or gets injected twice.
func TestBasePromptReachesProviderAsFirstSystemMessage(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "ok"})
	workDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workDir, "AGENTS.md"),
		[]byte("project rules"), 0o644,
	); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeInferenceConfig(t, configDir, provider.URL())

	res, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("headless.Run: %v", err)
	}
	if res.Status != "completed" || res.ExitCode != 0 {
		t.Fatalf("result = %+v, want completed/0", res)
	}

	msgs, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("LastMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatal("provider received no messages")
	}

	first, ok := msgs[0]["role"].(string)
	if !ok || first != "system" {
		t.Fatalf("first message role = %#v, want system", msgs[0]["role"])
	}
	firstText := messageText(t, msgs[0])
	if !strings.Contains(firstText, "You are opencraft") {
		t.Fatalf("first system message must carry the base identity, got: %q", firstText)
	}

	var systemText strings.Builder
	var sawEnvironment bool
	for i, m := range msgs {
		if role, _ := m["role"].(string); role == "system" {
			text := messageText(t, m)
			systemText.WriteString(text)
			systemText.WriteString("\n")
			if strings.Contains(text, "Workspace root:") {
				sawEnvironment = true
				if i == 0 {
					t.Fatalf("environment section must follow base identity, not precede it")
				}
			}
		}
	}
	if strings.Count(systemText.String(), "You are opencraft") != 1 {
		t.Fatalf("base identity must appear exactly once in system messages, got %q",
			systemText.String())
	}
	if !sawEnvironment {
		t.Fatalf("world-state environment section missing from provider messages: %q",
			systemText.String())
	}
	// Every base fragment must reach the provider, not just identity:
	// one marker per fragment guards against a future refactor that
	// drops a section from the channel.
	for _, marker := range []string{
		"You are opencraft",                  // base_identity
		"Core tools are always visible",      // base_work
		"## Sandbox commands",                // base_sandbox
		"## Editing constraints",             // base_editing
		"AGENTS.md and project instructions", // base_agents
		"Multi-agent collaboration",          // base_special
		"Respond in the user's language",     // base_format
	} {
		if !strings.Contains(systemText.String(), marker) {
			t.Fatalf("base fragment marker %q missing from system messages: %q",
				marker, systemText.String())
		}
	}

	// AGENTS.md is user-authored standing guidance and must sit right
	// after the permissions section, before any later ephemeral state.
	permsAt, agentsAt := -1, -1
	for i, m := range msgs {
		role, _ := m["role"].(string)
		text := messageText(t, m)
		if role == "system" && strings.Contains(text, "Permission profile:") {
			permsAt = i
		}
		if strings.Contains(text, "project rules") {
			agentsAt = i
		}
	}
	if permsAt < 0 || agentsAt != permsAt+1 {
		t.Fatalf("AGENTS.md must follow permissions (permsAt=%d agentsAt=%d): %q",
			permsAt, agentsAt, systemText.String())
	}
	// Roles must be grouped: no system message may follow the first
	// user-role content (AGENTS.md here).
	sawUser := false
	for _, m := range msgs {
		role, _ := m["role"].(string)
		if role == "user" {
			sawUser = true
		} else if role == "system" && sawUser {
			t.Fatalf("system message follows user content: %q", systemText.String())
		}
	}

	last := msgs[len(msgs)-1]
	if role, _ := last["role"].(string); role != "user" {
		t.Fatalf("last message role = %q, want user turn", role)
	}
	if text := messageText(t, last); !strings.Contains(text, "hello") {
		t.Fatalf("last user message = %q, want the submitted prompt", text)
	}
}

// messageText extracts the plain text of one provider message. Chat
// requests carry content as a string; tolerate an array shape so a
// driver change does not silently produce empty assertions.
func messageText(t *testing.T, m map[string]any) string {
	t.Helper()
	switch c := m["content"].(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, p := range c {
			if part, ok := p.(map[string]any); ok {
				if text, ok := part["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		t.Fatalf("message content has unexpected shape %T: %#v", m["content"], m)
		return ""
	}
}
