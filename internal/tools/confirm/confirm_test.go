package confirm

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"

	"github.com/GizClaw/opencraft/internal/runtime"
)

func confirmCtx(t *testing.T, choice string, cancelled bool) context.Context {
	t.Helper()
	meta := map[string]string{}
	if cancelled {
		meta[runtime.MetaStatus] = string(runtime.ReplyCancelled)
	} else {
		meta[runtime.MetaChoice] = choice
	}
	return agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{Metadata: meta}, nil
		},
	})
}

func TestConfirmYes(t *testing.T) {
	ok, err := Confirm(confirmCtx(t, "yes", false), "title", "body")
	if err != nil || !ok {
		t.Fatalf("Confirm(yes) = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestConfirmNo(t *testing.T) {
	ok, err := Confirm(confirmCtx(t, "no", false), "title", "body")
	if err != nil || ok {
		t.Fatalf("Confirm(no) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestConfirmCancel(t *testing.T) {
	ok, err := Confirm(confirmCtx(t, "", true), "title", "body")
	if err != nil || ok {
		t.Fatalf("Confirm(cancel) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestConfirmWithoutHostFailsClosed(t *testing.T) {
	if ok, err := Confirm(context.Background(), "title", "body"); err == nil || ok {
		t.Fatalf("Confirm(no host) = (%v, %v), want error", ok, err)
	}
}

func TestConfirmAskErrorFailsClosed(t *testing.T) {
	ctx := agent.ContextWithHost(context.Background(), agent.HostFuncs{
		AskUserFn: func(
			context.Context, agent.UserPrompt,
		) (agent.UserReply, error) {
			return agent.UserReply{}, context.Canceled
		},
	})
	if ok, err := Confirm(ctx, "title", "body"); err == nil || ok {
		t.Fatalf("Confirm(ask error) = (%v, %v), want error", ok, err)
	}
}
