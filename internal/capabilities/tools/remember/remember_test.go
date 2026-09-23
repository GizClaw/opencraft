package remember

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/assembly"
	"github.com/GizClaw/opencraft/internal/foundation/compat"
	"github.com/GizClaw/opencraft/internal/foundation/db"
)

func newMemory(t *testing.T) *userstore.Store {
	t.Helper()
	handle, err := db.Open(filepath.Join(t.TempDir(), "user.db"))
	if err != nil {
		t.Fatalf("open user db: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	if err := compat.User(context.Background(), handle); err != nil {
		t.Fatalf("migrate user db: %v", err)
	}
	store, err := userstore.Attach(handle)
	if err != nil {
		t.Fatalf("attach memory: %v", err)
	}
	return store
}

// newTool builds the tool over a migrated store with the given
// settings, approving every confirmation unless the test says otherwise.
func newTool(t *testing.T, settings Settings) (*Tool, *userstore.Store) {
	t.Helper()
	store := newMemory(t)
	if settings.WorkDir == "" {
		settings.WorkDir = "/work/project"
	}
	built, err := New(context.Background(), store, settings)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	built.confirm = func(context.Context, string, string) (bool, error) {
		return true, nil
	}
	return built, store
}

func execText(t *testing.T, built *Tool, args string) string {
	t.Helper()
	content, err := built.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute(%s): %v", args, err)
	}
	return content.Text()
}

type envelopeOut struct {
	Applied []struct {
		Op    string `json:"op"`
		ID    string `json:"id"`
		Scope string `json:"scope"`
		Text  string `json:"text"`
	} `json:"applied"`
	Note string `json:"note"`
}

func decodeEnvelope(t *testing.T, text string) envelopeOut {
	t.Helper()
	var out envelopeOut
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("decode envelope %q: %v", text, err)
	}
	return out
}

// TestExecuteAddReplaceRemove covers the three operations end to end:
// add files the fact under the tool's workspace, replace rewrites the
// text of an existing fact, remove deletes it.
func TestExecuteAddReplaceRemove(t *testing.T) {
	built, store := newTool(t, Settings{})
	ctx := context.Background()

	out := decodeEnvelope(t, execText(t, built,
		`{"operations":[{"op":"add","text":"projects live under ~/Workspace",`+
			`"kind":"environment"}]}`))
	if len(out.Applied) != 1 || out.Applied[0].Op != OpAdd {
		t.Fatalf("applied = %+v", out.Applied)
	}
	if out.Applied[0].Scope != userstore.ScopeWorkspace {
		t.Fatalf("scope = %q, want the default workspace", out.Applied[0].Scope)
	}
	id := out.Applied[0].ID
	facts, err := store.List(ctx, userstore.Query{Workspace: "/work/project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Workspace != "/work/project" {
		t.Fatalf("facts = %+v", facts)
	}
	if facts[0].Kind != "environment" {
		t.Fatalf("kind = %q", facts[0].Kind)
	}

	out = decodeEnvelope(t, execText(t, built,
		`{"operations":[{"op":"replace","id":"`+id+`",`+
			`"text":"projects live under ~/Workspace/opencraft"}]}`))
	if len(out.Applied) != 1 || out.Applied[0].Text == "" ||
		!strings.Contains(out.Applied[0].Text, "opencraft") {
		t.Fatalf("replace applied = %+v", out.Applied)
	}

	out = decodeEnvelope(t, execText(t, built,
		`{"operations":[{"op":"remove","id":"`+id+`"}]}`))
	if len(out.Applied) != 1 || out.Applied[0].Op != OpRemove {
		t.Fatalf("remove applied = %+v", out.Applied)
	}
	facts, err = store.List(ctx, userstore.Query{Workspace: "/work/project"})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts after remove = %+v", facts)
	}
}

// TestExecuteGlobalScope covers the scope switch: a global fact carries
// no workspace, so it is injected for every project.
func TestExecuteGlobalScope(t *testing.T) {
	built, store := newTool(t, Settings{})
	out := decodeEnvelope(t, execText(t, built,
		`{"operations":[{"op":"add","scope":"global",`+
			`"text":"the user's preferred language is Chinese"}]}`))
	if len(out.Applied) != 1 || out.Applied[0].Scope != userstore.ScopeGlobal {
		t.Fatalf("applied = %+v", out.Applied)
	}
	facts, err := store.List(context.Background(), userstore.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Workspace != "" {
		t.Fatalf("facts = %+v, want one global fact", facts)
	}
}

// TestExecuteConfirmationDeclined covers the gate: a declined prompt
// stores nothing and reports why.
func TestExecuteConfirmationDeclined(t *testing.T) {
	built, store := newTool(t, Settings{})
	built.confirm = func(context.Context, string, string) (bool, error) {
		return false, nil
	}
	_, err := built.Execute(context.Background(),
		`{"operations":[{"op":"add","text":"not wanted"}]}`)
	if err == nil {
		t.Fatal("declined confirmation returned no error")
	}
	if !strings.Contains(err.Error(), "declined") {
		t.Fatalf("error = %v", err)
	}
	facts, listErr := store.List(context.Background(), userstore.Query{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(facts) != 0 {
		t.Fatalf("declined call stored %+v", facts)
	}
}

// TestConfirmationNamesEveryChange covers what the user is asked to
// approve: every operation, in order.
func TestConfirmationNamesEveryChange(t *testing.T) {
	built, _ := newTool(t, Settings{})
	var asked []string
	built.confirm = func(_ context.Context, title, body string) (bool, error) {
		asked = append(asked, title, body)
		return true, nil
	}
	first := decodeEnvelope(t, execText(t, built,
		`{"operations":[{"op":"add","text":"first fact"}]}`))
	if len(first.Applied) != 1 {
		t.Fatalf("applied = %+v", first.Applied)
	}
	asked = nil
	_ = execText(t, built, `{"operations":[
		{"op":"add","text":"second fact"},
		{"op":"remove","id":"`+first.Applied[0].ID+`"}]}`)
	if len(asked) != 2 {
		t.Fatalf("confirmation = %v", asked)
	}
	if asked[0] != "Remember this?" {
		t.Fatalf("title = %q", asked[0])
	}
	if !strings.Contains(asked[1], "second fact") ||
		!strings.Contains(asked[1], "Forget: "+first.Applied[0].ID) {
		t.Fatalf("body = %q", asked[1])
	}
}

// TestExecuteRejectsBadArguments covers the validation that keeps a
// half-written batch out of the store: every operation is checked before
// anything is written.
func TestExecuteRejectsBadArguments(t *testing.T) {
	built, store := newTool(t, Settings{})
	ctx := context.Background()
	cases := map[string]string{
		"empty list":         `{"operations":[]}`,
		"unknown op":         `{"operations":[{"op":"upsert","text":"x"}]}`,
		"missing text":       `{"operations":[{"op":"add"}]}`,
		"remove without id":  `{"operations":[{"op":"remove"}]}`,
		"replace without id": `{"operations":[{"op":"replace","text":"x"}]}`,
		"bad scope":          `{"operations":[{"op":"add","text":"x","scope":"project"}]}`,
		"bad json":           `{"operations":`,
	}
	for name, args := range cases {
		if _, err := built.Execute(ctx, args); err == nil {
			t.Fatalf("%s: no error for %s", name, args)
		}
	}
	// A batch where the first entry is fine and the second is not must
	// not write the first one either.
	_, err := built.Execute(ctx, `{"operations":[
		{"op":"add","text":"good fact"},
		{"op":"add"}]}`)
	if err == nil {
		t.Fatal("partially invalid batch was accepted")
	}
	facts, listErr := store.List(ctx, userstore.Query{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(facts) != 0 {
		t.Fatalf("invalid batch wrote %+v", facts)
	}
}

// TestExecuteBounds covers the two call bounds: the per-call operation
// limit and the per-fact text limit.
func TestExecuteBounds(t *testing.T) {
	built, _ := newTool(t, Settings{MaxOperations: 2, MaxTextChars: 40})
	ctx := context.Background()
	if _, err := built.Execute(ctx, `{"operations":[
		{"op":"add","text":"one"},
		{"op":"add","text":"two"},
		{"op":"add","text":"three"}]}`); err == nil {
		t.Fatal("operation count over the limit was accepted")
	}
	if _, err := built.Execute(ctx, `{"operations":[{"op":"add","text":"`+
		strings.Repeat("long ", 20)+`"}]}`); err == nil {
		t.Fatal("text over the limit was accepted")
	}
	if _, err := New(ctx, newMemory(t), Settings{MaxTextChars: 5}); err == nil {
		t.Fatal("out-of-range settings were accepted")
	}
	// A candidate the store itself would refuse (empty text) is refused
	// here too, with the operation index in the message.
	if _, err := built.Execute(ctx,
		`{"operations":[{"op":"add","text":"   "}]}`); err == nil {
		t.Fatal("blank text was accepted")
	}
}

// TestExecuteRefusesSecrets covers the write-path redaction: text that
// matches a configured secret rule is refused instead of being stored
// (a redacted fact would silently replace what the user said).
func TestExecuteRefusesSecrets(t *testing.T) {
	built, store := newTool(t, Settings{Redact: assembly.RedactSettings{
		Enabled: true,
		Rules: []assembly.RedactRuleSettings{
			{Pattern: `sk-[A-Za-z0-9]{16,}`},
		},
	}})
	_, err := built.Execute(context.Background(), `{"operations":[
		{"op":"add","text":"the key is sk-abcdefghijklmnopqrst"}]}`)
	if err == nil {
		t.Fatal("secret-looking text was stored")
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Fatalf("error = %v, want a secret explanation", err)
	}
	facts, listErr := store.List(context.Background(), userstore.Query{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(facts) != 0 {
		t.Fatalf("secret was stored: %+v", facts)
	}

	// With redaction disabled the same text stores: the rule is the
	// deployment's decision, not the tool's.
	plain, _ := newTool(t, Settings{})
	if out := execText(t, plain, `{"operations":[
		{"op":"add","text":"the key is sk-abcdefghijklmnopqrst"}]}`); out == "" {
		t.Fatal("empty envelope")
	}
}

// TestDefinitionAndMetadata covers the wire-facing surface: the schema
// names the operations, and the tool admits it mutates durable state.
func TestDefinitionAndMetadata(t *testing.T) {
	built, _ := newTool(t, Settings{})
	def := built.Definition()
	if def.Name != Name {
		t.Fatalf("name = %q", def.Name)
	}
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(def.InputSchema, &schema); err != nil {
		t.Fatalf("parameters = %s: %v", def.InputSchema, err)
	}
	if _, ok := schema.Properties["operations"]; !ok {
		t.Fatalf("parameters = %s, want an operations property", def.InputSchema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "operations" {
		t.Fatalf("required = %v", schema.Required)
	}
	if !strings.Contains(def.Description, "long-term memory") {
		t.Fatalf("description = %q", def.Description)
	}
	if meta := built.Metadata(); !meta.MutatesState {
		t.Fatalf("metadata = %+v, want MutatesState", meta)
	}
	if _, ok := any(built).(tool.Tool); !ok {
		t.Fatal("Tool does not implement tool.Tool")
	}
}

// TestNewRefusesEmptyStore covers the contribution rule: a runtime
// without a user database has nowhere to remember anything.
func TestNewRefusesEmptyStore(t *testing.T) {
	if _, err := New(context.Background(), userstore.Empty(), Settings{}); err == nil {
		t.Fatal("empty store was accepted")
	}
	if _, err := New(context.Background(), nil, Settings{}); err == nil {
		t.Fatal("nil store was accepted")
	}
}

// TestExecuteContentIsText pins the result shape: one text part whose
// body is the JSON envelope, so the model reads the stored ids.
func TestExecuteContentIsText(t *testing.T) {
	built, _ := newTool(t, Settings{})
	content, err := built.Execute(context.Background(), `{"operations":[
		{"op":"add","text":"one durable fact"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(content.Parts) != 1 {
		t.Fatalf("parts = %+v", content.Parts)
	}
	if _, ok := content.Parts[0].(message.TextPart); !ok {
		t.Fatalf("part = %T, want a text part", content.Parts[0])
	}
}
