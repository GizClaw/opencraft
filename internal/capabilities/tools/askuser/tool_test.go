package askuser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/foundation/interact"
)

func TestDefinition(t *testing.T) {
	def := New().Definition()
	if def.Name != Name {
		t.Errorf("name = %s", def.Name)
	}
	for _, prop := range []string{"question", "kind", "options"} {
		if !strings.Contains(string(def.InputSchema), "\""+prop+"\"") {
			t.Errorf("schema missing %s: %s", prop, def.InputSchema)
		}
	}
}

func TestExecuteText(t *testing.T) {
	var got agent.UserPrompt
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		got = prompt
		return agent.UserReply{
			Parts: []message.Part{message.TextPart{Text: "main.go"}},
		}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	out, err := New().Execute(ctx, `{"question":"Which file?"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata[interact.MetaKind] != string(interact.KindText) {
		t.Errorf("kind = %+v", got.Metadata)
	}
	var res struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Text != "main.go" {
		t.Errorf("out = %q err = %v", out, err)
	}
}

func TestExecuteSelectMapsChoice(t *testing.T) {
	var got agent.UserPrompt
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		got = prompt
		return agent.UserReply{
			Metadata: map[string]string{
				interact.MetaChoice: "方案 B",
			},
		}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	out, err := New().Execute(ctx,
		`{"question":"选一个","kind":"select","options":["方案 A","方案 B"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata[interact.MetaKind] != string(interact.KindSelect) ||
		!strings.Contains(got.Metadata[interact.MetaOptions], "方案 B") {
		t.Errorf("prompt = %+v", got)
	}
	var res struct {
		Choice string `json:"choice"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Choice != "方案 B" {
		t.Errorf("out = %q err = %v", out, err)
	}
}

func TestExecuteConfirmDefaults(t *testing.T) {
	var got agent.UserPrompt
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		got = prompt
		return agent.UserReply{
			Metadata: map[string]string{interact.MetaChoice: "no"},
		}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	if _, err := New().Execute(ctx, `{"question":"继续?","kind":"confirm"}`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Metadata[interact.MetaOptions], "\"yes\"") {
		t.Errorf("confirm options = %s", got.Metadata[interact.MetaOptions])
	}
}

func TestExecuteSelectMultiAndOther(t *testing.T) {
	var got agent.UserPrompt
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		got = prompt
		return agent.UserReply{
			Metadata: map[string]string{
				interact.MetaChoices: `["方案 A","方案 C"]`,
				interact.MetaOther:   "我的想法",
			},
		}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	out, err := New().Execute(ctx,
		`{"question":"选哪些","kind":"select","options":["方案 A","方案 B","方案 C"],"multiple":true}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Metadata[interact.MetaMulti] != "true" {
		t.Errorf("prompt = %+v", got)
	}
	var res struct {
		Choices []string `json:"choices"`
		Other   string   `json:"other"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Choices) != 2 || res.Other != "我的想法" {
		t.Errorf("out = %q", out)
	}
}

func TestExecuteSelectDisableOther(t *testing.T) {
	var got agent.UserPrompt
	host := agent.HostFuncs{AskUserFn: func(
		_ context.Context, prompt agent.UserPrompt,
	) (agent.UserReply, error) {
		got = prompt
		return agent.UserReply{Metadata: map[string]string{
			interact.MetaChoice: "a",
		}}, nil
	}}
	ctx := agent.ContextWithHost(context.Background(), host)
	if _, err := New().Execute(ctx,
		`{"question":"q","kind":"select","options":["a"],"allow_other":false}`); err != nil {
		t.Fatal(err)
	}
	if got.Metadata[interact.MetaAllowOther] != "false" {
		t.Errorf("prompt = %+v", got)
	}
}

func TestExecuteValidation(t *testing.T) {
	ctx := agent.ContextWithHost(context.Background(), agent.NoopHost{})
	for _, args := range []string{
		`{}`,
		`{"question":"q","kind":"bogus"}`,
		`{"question":"q","kind":"select"}`,
		`{"question":"q","kind":"text","multiple":true}`,
	} {
		if _, err := New().Execute(ctx, args); err == nil {
			t.Errorf("Execute(%s) should fail", args)
		}
	}
	if _, err := New().Execute(context.Background(),
		`{"question":"q"}`); err == nil {
		t.Error("Execute without host should fail")
	}
}
