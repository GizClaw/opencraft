package e2e_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/headless"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// writeUserFact stores one fact in the user database the state root
// already holds, the way an accepted review suggestion or the settings
// page would. The headless run wires the same store, so the fact has to
// show up in the next session's world section.
func writeUserFact(t *testing.T, dataDir string, fact userstore.Fact) {
	t.Helper()
	handle, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	defer func() {
		if err := handle.Close(); err != nil {
			t.Errorf("close user db: %v", err)
		}
	}()
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := userstore.Attach(handle)
	if err != nil {
		t.Fatalf("attach memory store: %v", err)
	}
	if _, err := store.Add(context.Background(), fact); err != nil {
		t.Fatalf("add fact: %v", err)
	}
}

// TestHeadlessUserMemoryInjectsStoredFacts drives the long-term memory
// path end to end: a fact living in the state root's user database is
// injected into a fresh session's world section before its first model
// call — the property that makes it "long-term" rather than
// conversation state.
func TestHeadlessUserMemoryInjectsStoredFacts(t *testing.T) {
	const fact = "the release checklist lives in docs/release.md"
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The state root is the config directory's parent, so this is the
	// same user.db the run opens.
	writeUserFact(t, filepath.Dir(configDir), userstore.Fact{
		Text:  fact,
		Scope: userstore.ScopeGlobal,
		Kind:  "convention",
	})

	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "Ready."})
	writeFakeInferenceConfig(t, configDir, provider.URL())

	result, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "What is on the release checklist?",
		Quiet:     true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("run status = %q", result.Status)
	}

	messages, err := provider.LastMessages()
	if err != nil {
		t.Fatalf("last messages: %v", err)
	}
	if !messagesCarry(messages, "## Long-term memory") {
		t.Fatalf("the session has no long-term memory section:\n%v", messages)
	}
	if !messagesCarry(messages, fact) {
		t.Fatalf("the session's request does not carry the stored fact:\n%v",
			messages)
	}
}

// TestHeadlessUserMemoryHonoursWorkspaceScope pins the scope boundary
// the way an e2e can see it: a workspace-scoped fact reaches the
// workspace it was filed under, and no other.
func TestHeadlessUserMemoryHonoursWorkspaceScope(t *testing.T) {
	const fact = "this workspace deploys to staging-7"
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner := realPath(t, t.TempDir())
	other := realPath(t, t.TempDir())
	writeUserFact(t, filepath.Dir(configDir), userstore.Fact{
		Text:      fact,
		Scope:     userstore.ScopeWorkspace,
		Workspace: owner,
	})

	provider := fakeprovider.New(t,
		fakeprovider.Reply{Text: "Ready."},
		fakeprovider.Reply{Text: "Ready."},
	)
	writeFakeInferenceConfig(t, configDir, provider.URL())

	for _, dir := range []string{owner, other} {
		if _, err := headless.Run(context.Background(), headless.Options{
			WorkDir:   dir,
			ConfigDir: configDir,
			Prompt:    "Anything to know about deploys?",
			Quiet:     true,
		}); err != nil {
			t.Fatalf("run in %s: %v", dir, err)
		}
		messages, err := provider.LastMessages()
		if err != nil {
			t.Fatalf("last messages: %v", err)
		}
		found := messagesCarry(messages, fact)
		if dir == owner && !found {
			t.Fatalf("the owning workspace does not see its own fact:\n%v",
				messages)
		}
		if dir == other && found {
			t.Fatalf("a workspace-scoped fact leaked into another workspace:\n%v",
				messages)
		}
	}
}

// realPath resolves a temp directory the way the runtime keys
// workspaces, so the fact is filed under the path the run reports.
func realPath(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	return resolved
}

// messagesCarry reports whether any request message contains text.
func messagesCarry(messages []map[string]any, text string) bool {
	for _, msg := range messages {
		if raw, ok := msg["content"].(string); ok && strings.Contains(raw, text) {
			return true
		}
		// Content parts: a string inside the array is enough.
		if parts, ok := msg["content"].([]any); ok {
			for _, part := range parts {
				if s, ok := part.(string); ok && strings.Contains(s, text) {
					return true
				}
				if obj, ok := part.(map[string]any); ok {
					if s, ok := obj["text"].(string); ok && strings.Contains(s, text) {
						return true
					}
				}
			}
		}
	}
	return false
}
