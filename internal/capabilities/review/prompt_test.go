package review

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
)

// toolResultMessage is one tool-result message: the shape the review
// counts as a turn's real work volume.
func toolResultMessage() message.Message {
	return message.Message{
		Role: message.RoleTool,
		Content: message.Content{Parts: []message.Part{
			message.ToolResultPart{Result: message.ToolResult{
				CallID: "c1", Content: message.NewTextContent("ok"),
			}},
		}},
	}
}

func TestParseCandidatesBareJSON(t *testing.T) {
	got, err := parseCandidates(
		`{"memory":[{"text":"prefers tabs","scope":"global","kind":"preference","reason":"stated"}]}`,
		0,
	)
	if err != nil {
		t.Fatalf("parseCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1", len(got))
	}
	if got[0].Text != "prefers tabs" || got[0].Scope != userstore.ScopeGlobal ||
		got[0].Kind != "preference" || got[0].Reason != "stated" {
		t.Fatalf("candidate = %+v", got[0])
	}
}

func TestParseCandidatesToleratesFenceAndProse(t *testing.T) {
	got, err := parseCandidates(
		"Sure, here you go:\n```json\n{\"memory\":[{\"text\":\"uses fzf\"}]}\n```\nHope that helps.",
		0,
	)
	if err != nil {
		t.Fatalf("parseCandidates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %+v, want one parsed out of the prose", got)
	}
	// A missing scope is the global default; a missing kind stays empty
	// and the store fills its own default on write.
	if got[0].Scope != userstore.ScopeGlobal || got[0].Kind != "" {
		t.Fatalf("candidate = %+v, want the defaults", got[0])
	}
}

// TestParseCandidatesNormalizesScopeAndKind pins the whitelist: a known
// scope maps to the store constant, an absent one is global, and an
// unknown one falls back to the narrower workspace scope instead of
// being dropped.
func TestParseCandidatesNormalizesScopeAndKind(t *testing.T) {
	got, err := parseCandidates(
		`{"memory":[`+
			`{"text":"a","scope":"WORKSPACE","kind":"Preference"},`+
			`{"text":"b","scope":"weird"},`+
			`{"text":"c"},`+
			`{"text":"d","scope":"GLOBAL"}]}`,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantScopes := []string{
		userstore.ScopeWorkspace, userstore.ScopeWorkspace,
		userstore.ScopeGlobal, userstore.ScopeGlobal,
	}
	if len(got) != len(wantScopes) {
		t.Fatalf("candidates = %d, want %d", len(got), len(wantScopes))
	}
	for i, want := range wantScopes {
		if got[i].Scope != want {
			t.Fatalf("candidate %d scope = %q, want %q", i, got[i].Scope, want)
		}
	}
	if got[0].Kind != "preference" {
		t.Fatalf("kind = %q, want it lowercased", got[0].Kind)
	}
}

func TestParseCandidatesSkipsEmptyTextAndTruncates(t *testing.T) {
	answer := `{"memory":[{"text":"  "},{"text":"one"},{"text":"two"},{"text":"three"}]}`

	capped, err := parseCandidates(answer, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 2 || capped[0].Text != "one" || capped[1].Text != "two" {
		t.Fatalf("capped = %+v, want the first two non-empty items", capped)
	}

	all, err := parseCandidates(answer, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("unlimited = %+v, want the empty item skipped", all)
	}
}

// TestParseCandidatesIgnoresUnknownKeysAndEscapes pins the two things a
// chatty model answer must not cost us: extra keys inside an item are
// dropped (never a decode failure), and escaped quotes in the text are
// decoded rather than mis-terminating the string scan.
func TestParseCandidatesIgnoresUnknownKeysAndEscapes(t *testing.T) {
	answer := `{"memory":[{"text":"say \"hi\"","confidence":0.9,"extra":{"x":1}}]}`
	got, err := parseCandidates(answer, 0)
	if err != nil {
		t.Fatalf("parseCandidates: %v", err)
	}
	if len(got) != 1 || got[0].Text != `say "hi"` {
		t.Fatalf("candidates = %+v, want one decoded candidate", got)
	}
}

func TestParseCandidatesErrorsOnMalformedAnswer(t *testing.T) {
	for _, answer := range []string{
		"no json here",
		`{"memory":`,
		`{"memory":"nope"}`,
		`{oops}`,
	} {
		if _, err := parseCandidates(answer, 0); err == nil {
			t.Fatalf("parseCandidates(%q) returned no error", answer)
		}
	}
	// A well-formed answer without candidates is not an error: an
	// ordinary turn legitimately proposes nothing.
	got, err := parseCandidates(`{"foo":1}`, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("parseCandidates(no memory key) = %+v, %v", got, err)
	}
}

func TestExtractJSONObjectPicksTheFirstBalancedObject(t *testing.T) {
	got, err := extractJSONObject(`prefix {"a":{"b":1}} suffix {"c":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"a":{"b":1}}` {
		t.Fatalf("extracted %q, want the first balanced object", got)
	}

	// Braces inside a string must not close the object.
	got, err = extractJSONObject(`{"text":"a}b"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got != `{"text":"a}b"}` {
		t.Fatalf("extracted %q, want the brace in the string ignored", got)
	}

	if _, err := extractJSONObject("no braces at all"); err == nil {
		t.Fatal("extractJSONObject must fail when the answer has no object")
	}
	if _, err := extractJSONObject(`{"unterminated":`); err == nil {
		t.Fatal("extractJSONObject must fail on an unterminated object")
	}
}

func TestBuildPromptBoundsFactsSkillsAndMaxItems(t *testing.T) {
	in := buildPrompt(promptInput{
		Facts:    make([]userstore.Fact, maxPromptFacts+5),
		Skills:   make([]string, maxPromptSkills+5),
		MaxItems: 0,
	})
	if len(in.Facts) != maxPromptFacts {
		t.Fatalf("facts = %d, want the %d cap", len(in.Facts), maxPromptFacts)
	}
	if len(in.Skills) != maxPromptSkills {
		t.Fatalf("skills = %d, want the %d cap", len(in.Skills), maxPromptSkills)
	}
	if in.MaxItems != 1 {
		t.Fatalf("max items = %d, want a zero to mean one", in.MaxItems)
	}
	if got := buildPrompt(promptInput{MaxItems: 4}).MaxItems; got != 4 {
		t.Fatalf("max items = %d, want an explicit value kept", got)
	}
}

func TestContextMessagesCarryTheItemLimit(t *testing.T) {
	msgs := promptInput{MaxItems: 3}.contextMessages()
	if len(msgs) != 1 || msgs[0].Role != message.RoleSystem {
		t.Fatalf("messages = %+v, want one system message", msgs)
	}
	if !strings.Contains(msgs[0].Content.Text(), "At most 3 item(s)") {
		t.Fatalf("system prompt does not carry the limit:\n%s", msgs[0].Content.Text())
	}
	zero := promptInput{}.contextMessages()
	if !strings.Contains(zero[0].Content.Text(), "At most 1 item(s)") {
		t.Fatalf("a zero limit must read as one, got:\n%s", zero[0].Content.Text())
	}
}

func TestUserTextCarriesTurnMemoryAndSkills(t *testing.T) {
	p := promptInput{
		Turn: runTurn{
			Request:   "fix the build",
			Answer:    "done",
			Tools:     []string{"exec_command", "files"},
			ToolCalls: 2,
			Status:    "completed",
		},
		Facts: []userstore.Fact{
			{Scope: userstore.ScopeWorkspace, Text: "runs wails3 task package"},
			{Scope: userstore.ScopeGlobal, Text: "prefers ripgrep"},
		},
		Skills:  []string{"flowcraft-config"},
		WorkDir: "/w/repo",
	}
	text := p.userText()
	for _, want := range []string{
		"/w/repo",
		"Status: completed",
		"Tool calls: 2",
		"exec_command, files",
		"fix the build",
		"done",
		"[workspace] runs wails3 task package",
		"[global] prefers ripgrep",
		"flowcraft-config",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, text)
		}
	}

	// An empty turn degrades to explicit placeholders instead of
	// rendering dangling headings.
	bare := promptInput{Turn: runTurn{Status: "completed"}, WorkDir: "/w"}.userText()
	if !strings.Contains(bare, "(none)") {
		t.Fatalf("an empty memory list must render (none):\n%s", bare)
	}
	for _, unwanted := range []string{"User request:", "Assistant answer:", "Installed skills"} {
		if strings.Contains(bare, unwanted) {
			t.Fatalf("empty section %q was rendered:\n%s", unwanted, bare)
		}
	}
	if !strings.Contains(promptInput{}.userText(), "(unknown)") {
		t.Fatal("a missing workspace must render (unknown)")
	}
}

// TestExcerptTruncatesOnRuneBoundary pins the byte-limit semantics: the
// limit is compared in bytes, the cut happens on runes, and a text whose
// rune count already fits is never touched (so multibyte text is not
// silently halved).
func TestExcerptTruncatesOnRuneBoundary(t *testing.T) {
	if got := excerpt("hello", 100); got != "hello" {
		t.Fatalf("short text = %q, want it untouched", got)
	}

	long := strings.Repeat("世", 2100)
	got := excerpt(long, 2000)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated text must be elided, got %q", got[len(got)-4:])
	}
	if runes := []rune(got); len(runes) != 2001 {
		t.Fatalf("truncated runes = %d, want 2000 + the ellipsis", len(runes))
	}

	// 1500 runes of multibyte text exceed the 2000-byte limit but fit
	// the rune budget, so the excerpt is returned whole.
	fits := strings.Repeat("世", 1500)
	if got := excerpt(fits, 2000); got != fits {
		t.Fatalf("excerpt changed a text that fits in runes (len %d)", len(got))
	}
}

func TestProjectTurnKeepsRequestAnswerToolsAndStatus(t *testing.T) {
	res := boardTurn(agent.StatusCompleted,
		message.NewTextMessage(message.RoleUser, "first"),
		message.NewTextMessage(message.RoleUser, "second"),
		message.Message{
			Role: message.RoleAssistant,
			Content: message.Content{Parts: []message.Part{
				message.ToolCallPart{Call: message.ToolCall{Name: "exec_command"}},
				message.ToolCallPart{Call: message.ToolCall{Name: "exec_command"}},
				message.ToolCallPart{Call: message.ToolCall{Name: "files"}},
			}},
		},
		toolResultMessage(),
		toolResultMessage(),
		toolResultMessage(),
		message.NewTextMessage(message.RoleAssistant, "answer one"),
		message.NewTextMessage(message.RoleAssistant, "answer two"),
	)
	turn := projectTurn(res)
	if turn.Request != "first\nsecond" {
		t.Fatalf("request = %q, want every user message joined", turn.Request)
	}
	if turn.Answer != "answer two" {
		t.Fatalf("answer = %q, want the last assistant text", turn.Answer)
	}
	if turn.ToolCalls != 3 {
		t.Fatalf("tool calls = %d, want the three tool results counted", turn.ToolCalls)
	}
	if turn.Status != "completed" {
		t.Fatalf("status = %q", turn.Status)
	}
	if got := strings.Join(turn.Tools, ","); got != "exec_command,files" {
		t.Fatalf("tools = %q, want deduped names in order", got)
	}
}

// TestProjectTurnReadsTheBoardNotTheTrailingBlock pins the shape the
// review shipped broken on: Result.Messages is the turn's trailing
// assistant block, so a projection that read it found no request, no
// tools and zero tool calls in a turn that ran four of them — and the
// min_tool_calls gate then skipped every successful turn forever.
func TestProjectTurnReadsTheBoardNotTheTrailingBlock(t *testing.T) {
	res := toolTurn(4)
	if len(res.Messages) != 1 || res.Messages[0].Role != message.RoleAssistant {
		t.Fatalf("fixture must carry the engine's narrowed tail, got %+v",
			res.Messages)
	}
	turn := projectTurn(res)
	if turn.Request != "do the thing" {
		t.Fatalf("request = %q, want the board's user message", turn.Request)
	}
	if turn.Answer != "done" {
		t.Fatalf("answer = %q, want the closing assistant text", turn.Answer)
	}
	if turn.ToolCalls != 4 {
		t.Fatalf("tool calls = %d, want the four the board holds", turn.ToolCalls)
	}
	if got := strings.Join(turn.Tools, ","); got != "exec_command" {
		t.Fatalf("tools = %q, want the called tool once", got)
	}

	// The world-state prefix is context the review must not read as the
	// turn's own request.
	for _, unwanted := range []string{"base prompt", "Long-term memory"} {
		if strings.Contains(turn.Request, unwanted) {
			t.Fatalf("request carries seeded context %q: %q", unwanted, turn.Request)
		}
	}

	// A projection without a board and without a tail degrades to empty
	// instead of panicking: the review skips a turn it cannot see.
	bare := projectTurn(&agent.Result{Status: agent.StatusCompleted})
	if bare.Request != "" || bare.Answer != "" || bare.ToolCalls != 0 {
		t.Fatalf("projection of an empty result = %+v, want the zero value", bare)
	}
}

// TestApplyMemoryIsTheSingleAcceptedWritePath pins where an accepted
// suggestion lands: through the store (dedupe, limits and provenance
// applied once), with the workspace scope filed under the caller's
// workspace and the global scope under none.
func TestApplyMemoryIsTheSingleAcceptedWritePath(t *testing.T) {
	ctx := context.Background()
	mem := newMemoryStore(t)

	fact, err := ApplyMemory(ctx, mem, "/w", json.RawMessage(
		`{"text":"uses tabs","scope":"workspace","kind":"preference"}`))
	if err != nil {
		t.Fatalf("ApplyMemory: %v", err)
	}
	if fact.Scope != userstore.ScopeWorkspace || fact.Workspace != "/w" {
		t.Fatalf("fact = %+v, want a fact filed under /w", fact)
	}
	if fact.Text != "uses tabs" || fact.Kind != "preference" {
		t.Fatalf("fact = %+v", fact)
	}

	global, err := ApplyMemory(ctx, mem, "/w", json.RawMessage(`{"text":"prefers ripgrep"}`))
	if err != nil {
		t.Fatal(err)
	}
	if global.Scope != userstore.ScopeGlobal || global.Workspace != "" {
		t.Fatalf("global fact = %+v, want no workspace", global)
	}

	// Accepting the same suggestion twice updates the row instead of
	// adding a twin: the store, not the caller, owns dedupe.
	if _, err := ApplyMemory(ctx, mem, "/w", json.RawMessage(
		`{"text":"uses tabs","scope":"workspace","kind":"preference"}`)); err != nil {
		t.Fatal(err)
	}
	facts, err := mem.List(ctx, userstore.Query{Workspace: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 2 {
		t.Fatalf("facts = %d, want two distinct rows", len(facts))
	}
}

func TestApplyMemoryRejectsBadPayloadAndMissingDatabase(t *testing.T) {
	ctx := context.Background()
	mem := newMemoryStore(t)
	if _, err := ApplyMemory(ctx, mem, "/w", json.RawMessage("not json")); !errdefs.IsValidation(err) {
		t.Fatalf("bad payload err = %v, want a validation error", err)
	}
	if _, err := ApplyMemory(ctx, nil, "/w", json.RawMessage(`{"text":"x"}`)); !errdefs.IsNotAvailable(err) {
		t.Fatalf("nil store err = %v, want NotAvailable", err)
	}
	if _, err := ApplyMemory(ctx, userstore.Empty(), "/w", json.RawMessage(`{"text":"x"}`)); !errdefs.IsNotAvailable(err) {
		t.Fatalf("empty store err = %v, want NotAvailable", err)
	}
}
