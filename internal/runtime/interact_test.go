package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/event"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/runtime/session"
)

func TestFromPromptDefaultsToText(t *testing.T) {
	p := agent.UserPrompt{
		Parts: []message.Part{message.TextPart{Text: "Which file?"}},
	}
	spec := FromPrompt(p, "p1", "r1", "t1")
	if spec.ID != "p1" || spec.Kind != KindText || spec.Title != "Which file?" {
		t.Errorf("spec = %+v", spec)
	}
}

func TestFromPromptMetadataConvention(t *testing.T) {
	opts, _ := json.Marshal([]Option{
		{Label: "方案 A", Value: "a"},
		{Label: "方案 B", Value: "b"},
	})
	p := agent.UserPrompt{
		Parts: []message.Part{message.TextPart{Text: "choose"}},
		Metadata: map[string]string{
			MetaKind:    string(KindSelect),
			MetaTitle:   "选择方案",
			MetaOptions: string(opts),
		},
	}
	spec := FromPrompt(p, "p1", "r1", "t1")
	if spec.Kind != KindSelect || spec.Title != "选择方案" {
		t.Fatalf("spec = %+v", spec)
	}
	if len(spec.Options) != 2 || spec.Options[1].Value != "b" {
		t.Errorf("options = %+v", spec.Options)
	}
}

func TestFromPromptMultiAndOther(t *testing.T) {
	p := agent.UserPrompt{
		Metadata: map[string]string{
			MetaKind:       string(KindSelect),
			MetaMulti:      "true",
			MetaAllowOther: "false",
		},
	}
	spec := FromPrompt(p, "p1", "r1", "t1")
	if !spec.Multi || spec.AllowOther {
		t.Errorf("spec = %+v", spec)
	}
	// AllowOther defaults to true when absent.
	if spec := FromPrompt(agent.UserPrompt{
		Metadata: map[string]string{MetaKind: string(KindSelect)},
	}, "p2", "r1", "t1"); !spec.AllowOther {
		t.Errorf("default allow_other = %+v", spec)
	}
}

func TestToUserReplyText(t *testing.T) {
	reply := ToUserReply(Reply{
		ID:     "p1",
		Status: ReplyOK,
		Text:   "main.go",
	})
	if len(reply.Parts) != 1 ||
		reply.Parts[0].(message.TextPart).Text != "main.go" {
		t.Errorf("reply = %+v", reply)
	}
	if reply.Metadata[MetaStatus] != string(ReplyOK) {
		t.Errorf("metadata = %+v", reply.Metadata)
	}
}

func TestToUserReplyOption(t *testing.T) {
	v := "a"
	reply := ToUserReply(Reply{ID: "p1", Status: ReplyOK, Option: &v})
	if reply.Metadata[MetaChoice] != "a" {
		t.Errorf("metadata = %+v", reply.Metadata)
	}
	if len(reply.Parts) != 1 ||
		reply.Parts[0].(message.TextPart).Text != "a" {
		t.Errorf("parts = %+v", reply.Parts)
	}
}

func TestToUserReplyMultiAndOther(t *testing.T) {
	reply := ToUserReply(Reply{
		ID:      "p1",
		Status:  ReplyOK,
		Options: []string{"a", "b"},
		Text:    "custom",
	})
	if reply.Metadata[MetaOther] != "custom" {
		t.Errorf("metadata = %+v", reply.Metadata)
	}
	var choices []string
	if err := json.Unmarshal([]byte(reply.Metadata[MetaChoices]), &choices); err != nil {
		t.Fatal(err)
	}
	if len(choices) != 2 || choices[1] != "b" {
		t.Errorf("choices = %+v", choices)
	}
	if len(reply.Parts) != 1 ||
		reply.Parts[0].(message.TextPart).Text != "custom" {
		t.Errorf("parts = %+v", reply.Parts)
	}
}

type fakeRuntime struct {
	patterns []event.Pattern
	sinks    []event.Sink
}

func (f *fakeRuntime) Attach(
	_ context.Context,
	pattern event.Pattern,
	sink event.Sink,
	_ ...event.AttachOption,
) (func(), error) {
	f.patterns = append(f.patterns, pattern)
	f.sinks = append(f.sinks, sink)
	return func() {}, nil
}

func (f *fakeRuntime) publish(t *testing.T, payload any, subject event.Subject) {
	t.Helper()
	env, err := event.NewEnvelope(context.Background(), subject, payload)
	if err != nil {
		t.Fatal(err)
	}
	for i, pattern := range f.patterns {
		if !pattern.Matches(subject) {
			continue
		}
		if err := f.sinks[i].OnEnvelope(context.Background(), env); err != nil {
			t.Fatal(err)
		}
	}
}

type fakeBackend struct {
	mu    sync.Mutex
	specs []Spec
	reply Reply
	err   error
}

func (b *fakeBackend) Ask(_ context.Context, spec Spec) (Reply, error) {
	b.mu.Lock()
	b.specs = append(b.specs, spec)
	reply := b.reply
	err := b.err
	b.mu.Unlock()
	return reply, err
}

type fakeReplier struct {
	mu    sync.Mutex
	id    string
	reply agent.UserReply
	err   error
}

func (r *fakeReplier) Reply(_ context.Context, promptID string, reply agent.UserReply) error {
	r.mu.Lock()
	r.id = promptID
	r.reply = reply
	err := r.err
	r.mu.Unlock()
	return err
}

func TestBrokerRoutesPromptToTurnReply(t *testing.T) {
	rt := &fakeRuntime{}
	backend := &fakeBackend{reply: Reply{Status: ReplyOK, Text: "hello"}}
	broker := New(rt, backend)
	if err := broker.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	turn := &fakeReplier{}
	broker.BindTurn("t1", turn)

	rt.publish(t, session.PromptRequested{
		RunID: "r1", TurnID: "t1", PromptID: "p1",
		Prompt: agent.UserPrompt{Parts: []message.Part{
			message.TextPart{Text: "say hi"},
		}},
	}, session.SubjectPromptRequested("r1"))

	deadline := time.Now().Add(time.Second)
	for {
		turn.mu.Lock()
		done := turn.id == "p1"
		turn.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply not delivered")
		}
		time.Sleep(time.Millisecond)
	}

	backend.mu.Lock()
	specs := backend.specs
	backend.mu.Unlock()
	if len(specs) != 1 || specs[0].Kind != KindText || specs[0].ID != "p1" {
		t.Errorf("specs = %+v", specs)
	}
	turn.mu.Lock()
	defer turn.mu.Unlock()
	if len(turn.reply.Parts) != 1 ||
		turn.reply.Parts[0].(message.TextPart).Text != "hello" {
		t.Errorf("reply = %+v", turn.reply)
	}
}

func TestBrokerBackendErrorResolvesEmpty(t *testing.T) {
	rt := &fakeRuntime{}
	backend := &fakeBackend{err: errTest}
	broker := New(rt, backend)
	if err := broker.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	turn := &fakeReplier{}
	broker.BindTurn("t1", turn)

	rt.publish(t, session.PromptRequested{
		RunID: "r1", TurnID: "t1", PromptID: "p1",
		Prompt: agent.UserPrompt{},
	}, session.SubjectPromptRequested("r1"))

	deadline := time.Now().Add(time.Second)
	for {
		turn.mu.Lock()
		done := turn.id == "p1"
		turn.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reply not delivered")
		}
		time.Sleep(time.Millisecond)
	}
	turn.mu.Lock()
	defer turn.mu.Unlock()
	if turn.reply.Metadata[MetaStatus] != string(ReplyCancelled) {
		t.Errorf("reply = %+v", turn.reply)
	}
}

func TestBrokerForwardsResolved(t *testing.T) {
	rt := &fakeRuntime{}
	backend := &resolvingBackend{
		ch:       make(chan session.PromptStatus, 1),
		reasonCh: make(chan string, 1),
	}
	broker := New(rt, backend)
	if err := broker.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	rt.publish(t, session.PromptResolved{
		RunID: "r1", TurnID: "t1", PromptID: "p1",
		Status: session.PromptExpired,
	}, session.SubjectPromptResolved("r1"))
	select {
	case status := <-backend.ch:
		if status != session.PromptExpired {
			t.Errorf("status = %s", status)
		}
	case <-time.After(time.Second):
		t.Fatal("resolved not forwarded")
	}
	select {
	case reason := <-backend.reasonCh:
		// No Ask ran for this prompt, so the reason falls back to a
		// label derived from the status.
		if reason != "context ended" {
			t.Errorf("reason = %q, want %q", reason, "context ended")
		}
	case <-time.After(time.Second):
		t.Fatal("reason not forwarded")
	}
}

func TestBrokerResolvedReasonFromAskError(t *testing.T) {
	rt := &fakeRuntime{}
	backend := &resolvingBackend{
		err:      context.DeadlineExceeded,
		ch:       make(chan session.PromptStatus, 1),
		reasonCh: make(chan string, 1),
	}
	broker := New(rt, backend)
	if err := broker.Attach(context.Background()); err != nil {
		t.Fatal(err)
	}
	turn := &fakeReplier{err: errors.New("prompt already resolved")}
	broker.BindTurn("t1", turn)

	rt.publish(t, session.PromptRequested{
		RunID: "r1", TurnID: "t1", PromptID: "p1",
		Prompt: agent.UserPrompt{},
	}, session.SubjectPromptRequested("r1"))

	// Wait for the ask goroutine to record the failure so the reason
	// is deterministically available when the resolution arrives.
	deadline := time.Now().Add(time.Second)
	for {
		broker.mu.Lock()
		_, ok := broker.reasons["p1"]
		broker.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ask error not recorded")
		}
		time.Sleep(time.Millisecond)
	}

	rt.publish(t, session.PromptResolved{
		RunID: "r1", TurnID: "t1", PromptID: "p1",
		Status: session.PromptExpired,
	}, session.SubjectPromptResolved("r1"))

	select {
	case status := <-backend.ch:
		if status != session.PromptExpired {
			t.Errorf("status = %s", status)
		}
	case <-time.After(time.Second):
		t.Fatal("resolved not forwarded")
	}
	select {
	case reason := <-backend.reasonCh:
		if reason != "timeout" {
			t.Errorf("reason = %q, want %q", reason, "timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("reason not forwarded")
	}
}

var errTest = context.Canceled

type resolvingBackend struct {
	err      error
	ch       chan session.PromptStatus
	reasonCh chan string
}

func (b *resolvingBackend) Ask(context.Context, Spec) (Reply, error) {
	return Reply{}, b.err
}

func (b *resolvingBackend) Resolve(
	_ context.Context,
	_ string,
	status session.PromptStatus,
	reason string,
) error {
	b.ch <- status
	if b.reasonCh != nil {
		b.reasonCh <- reason
	}
	return nil
}
