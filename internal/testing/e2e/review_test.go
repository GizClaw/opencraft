package e2e_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/adapters/headless"
	reviewstore "github.com/GizClaw/opencraft/internal/capabilities/review/store"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/db"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// reviewedFact is the fact the fake model proposes when the review call
// reaches it, in the JSON envelope the review prompt asks for.
const reviewedFact = "releases go out from a signed tag"

// reviewAnswer is that fact as the model's whole answer.
const reviewAnswer = `{"memory":[{"text":"` + reviewedFact +
	`","scope":"global","kind":"convention","reason":"stated in the turn"}]}`

// writeReviewEnabledConfig seeds the user layer with the fake inference
// instance plus a review switched on hard enough to fire on the first
// turn: a cadence of one turn and a tool floor of one, so the test does
// not have to script five tool-running turns to reach the gate.
func writeReviewEnabledConfig(t *testing.T, dir, baseURL string) {
	t.Helper()
	writeFakeInferenceConfig(t, dir, baseURL)
	enabled := true
	settings := config.ReviewSettings{
		Enabled:        &enabled,
		EveryTurns:     1,
		MinToolCalls:   1,
		MaxSuggestions: 3,
		TimeoutSeconds: config.ReviewMinTimeoutSeconds,
	}
	if err := config.SaveReview(dir, settings); err != nil {
		t.Fatalf("seed review settings: %v", err)
	}
}

// TestHeadlessReviewQueuesSuggestionAfterToolTurn drives the post-turn
// review the way it runs in production: a real turn goes through the
// real graph and uses a tool, the observe hook fires once the run ends,
// and one extra model call turns the finished turn into a suggestion
// waiting for the user's verdict in user.db.
//
// This is the path that was dead in production while the unit tests were
// green: the hook counted tool results on Result.Messages, which the
// engine narrows to the trailing assistant block, so every successful
// turn fell to the min_tool_calls gate and the review never ran.
func TestHeadlessReviewQueuesSuggestionAfterToolTurn(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"hello review\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
		fakeprovider.Reply{Text: reviewAnswer},
	)
	workDir := t.TempDir()
	configDir := filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeReviewEnabledConfig(t, configDir, provider.URL())

	result, err := headless.Run(context.Background(), headless.Options{
		WorkDir:   workDir,
		ConfigDir: configDir,
		Prompt:    "write out.txt",
		Quiet:     true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("run status = %q, want a completed turn", result.Status)
	}

	// The state root is the config directory's parent, so this is the
	// same user.db the run opened and the review queued into.
	rows := waitForSuggestions(t, filepath.Dir(configDir))
	var payload struct {
		Text  string `json:"text"`
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
		t.Fatalf("suggestion payload %s: %v", rows[0].Payload, err)
	}
	if payload.Text != reviewedFact {
		t.Fatalf("suggestion text = %q, want %q", payload.Text, reviewedFact)
	}
	if rows[0].SourceRun == "" || rows[0].SourceConversation == "" {
		t.Fatalf("suggestion %+v carries no provenance, want the reviewed run",
			rows[0])
	}

	// The turn has to reach the review, not just the queue: the call the
	// review makes must carry the user's request and the tool the turn
	// ran. A review that judges a blank page queues nothing worth
	// keeping, which is what reading Result.Messages produced.
	calls, err := provider.MessagesForCalls()
	if err != nil {
		t.Fatalf("provider request messages: %v", err)
	}
	for _, want := range []string{"User request:", "write out.txt", "write_file"} {
		if !anyCallCarries(calls, want) {
			t.Fatalf("no provider call carried %q; the review prompt is missing "+
				"the turn it reviewed:\n%s", want, summarizeCalls(calls))
		}
	}
}

// waitForSuggestions reads the queued pending suggestions, waiting for
// the detached review to land its row. The review outlives the turn it
// reviews by design, so the run returning is not a promise that the row
// is already there.
func waitForSuggestions(t *testing.T, dataDir string) []reviewstore.Suggestion {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rows, err := readPendingSuggestions(dataDir)
		if err == nil && len(rows) > 0 {
			return rows
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pending suggestion arrived (rows %d, err %v): "+
				"the post-turn review never queued anything", len(rows), err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// readPendingSuggestions opens the state root's user database the way
// the settings page does: attach a queue to an already-migrated handle
// and list what waits for the user's verdict.
func readPendingSuggestions(dataDir string) ([]reviewstore.Suggestion, error) {
	handle, err := db.Open(filepath.Join(dataDir, "user.db"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	if err := compat.User(context.Background(), handle); err != nil {
		return nil, err
	}
	queue, err := reviewstore.Attach(handle)
	if err != nil {
		return nil, err
	}
	return queue.List(context.Background(), reviewstore.StatusPending, 0)
}

// anyCallCarries reports whether any completion request sent text.
func anyCallCarries(calls [][]map[string]any, text string) bool {
	for _, messages := range calls {
		if messagesCarry(messages, text) {
			return true
		}
	}
	return false
}

// summarizeCalls renders what each request carried, so a failing
// assertion says which calls happened instead of only that one did not.
func summarizeCalls(calls [][]map[string]any) string {
	var b strings.Builder
	for i, messages := range calls {
		carried := make([]string, 0, len(messages))
		for _, msg := range messages {
			role, _ := msg["role"].(string)
			content, _ := msg["content"].(string)
			carried = append(carried, role+": "+strings.TrimSpace(content))
		}
		b.WriteString("- call ")
		b.WriteString(strings.TrimSpace(strings.Join(carried, " | ")))
		b.WriteString("\n")
		if i > 20 {
			break
		}
	}
	return b.String()
}
