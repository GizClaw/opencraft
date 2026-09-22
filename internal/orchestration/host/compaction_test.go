package host_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"

	"github.com/GizClaw/opencraft/internal/capabilities/worldstate"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
	"github.com/GizClaw/opencraft/internal/orchestration/interact"
	"github.com/GizClaw/opencraft/internal/testing/e2e/fakeprovider"
)

// TestRunExposesCompactionBookkeeping is the end-to-end half of the
// compaction contract: the graph's script node writes board vars, and the
// turn-end hook plus the desktop turn_end event read them by name. The
// string-level gate in foundation/config checks the names appear in the
// script; this test checks a real run actually produces them, because a
// renamed or never-stamped var is invisible to both languages' unit tests
// (it silently drops the usage anchor's fold generation and the UI's
// compaction notice).
func TestRunExposesCompactionBookkeeping(t *testing.T) {
	provider := fakeprovider.New(t,
		fakeprovider.Reply{ToolCalls: []fakeprovider.ToolCall{{
			Name:      "write_file",
			Arguments: `{"file_path":"out.txt","content":"ok\n"}`,
		}}},
		fakeprovider.Reply{Text: "done"},
	)
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "write out.txt"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.LastBoard == nil {
		t.Fatalf("result = %+v, want a final board", res)
	}

	report, ok := worldstate.CompactionReportFromBoard(res.LastBoard)
	if !ok {
		t.Fatal("the compaction node never stamped its bookkeeping on the board")
	}
	// A short turn fits, so nothing folded and nothing failed — the point
	// is that the counts are readable at all.
	if !report.Empty() {
		t.Fatalf("report = %+v, want an idle compaction report", report)
	}
	if _, ok := res.LastBoard.GetVar("world.compact.anchor_len"); !ok {
		t.Fatal("the node did not stamp the anchor length before the model call")
	}

	// The turn-end hook turns that stamp plus the provider's usage into the
	// persisted anchor the next turn folds against.
	anchor, err := h.Sessions().ReadUsageAnchor(run.ContextID())
	if err != nil {
		t.Fatalf("read usage anchor: %v", err)
	}
	if !anchor.Valid() {
		t.Fatalf("anchor = %+v, want the measured prompt size", anchor)
	}
	if anchor.AnchoredMessages <= 0 {
		t.Fatalf("anchor covers %d messages, want the stamped channel length",
			anchor.AnchoredMessages)
	}
	// The stamped length can only shrink afterwards (a fold moves messages
	// off the channel), never grow.
	if got := res.LastBoard.ChannelLen(agent.MainChannel); got < anchor.AnchoredMessages {
		t.Fatalf("channel holds %d messages, fewer than the measured %d",
			got, anchor.AnchoredMessages)
	}
}

// TestRunWithoutProviderUsageWritesNoAnchor pins the other half of the
// measurement contract: a provider that reports no usage leaves the
// conversation without an anchor, and the next turn falls back to the
// character estimate instead of folding against a zero. The fake provider
// reports usage by default (the streaming path would otherwise never
// exercise the accounting at all), so the no-usage shape needs its own run.
func TestRunWithoutProviderUsageWritesNoAnchor(t *testing.T) {
	provider := fakeprovider.New(t, fakeprovider.Reply{Text: "done"}).WithoutUsage()
	workDir := t.TempDir()
	dataDir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dataDir, "home"))
	configDir := filepath.Join(dataDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFakeConfig(t, configDir, provider.URL())

	mgr := host.NewManagerAt(dataDir, configDir)
	ctx := context.Background()
	h, err := mgr.Acquire(ctx, workDir, interact.Auto{}, nil)
	if err != nil {
		t.Fatalf("acquire host: %v", err)
	}
	defer func() { _ = h.Close() }()

	run, err := h.StartRun(ctx, host.RunOptions{
		Message: message.NewTextMessage(message.RoleUser, "hello"),
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	res, err := run.Wait(ctx)
	if err != nil {
		t.Fatalf("wait run: %v", err)
	}
	if res == nil || res.LastBoard == nil {
		t.Fatalf("result = %+v, want a final board", res)
	}
	// The node still stamps the shape of the request it sent; what it cannot
	// know is how many tokens that cost.
	if _, ok := res.LastBoard.GetVar("world.compact.anchor_len"); !ok {
		t.Fatal("the node must stamp the anchor length even without provider usage")
	}
	if _, err := h.Sessions().ReadUsageAnchor(run.ContextID()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("anchor read error = %v, want no anchor written", err)
	}
	// The turn itself is unaffected: a missing measurement costs estimation
	// accuracy, never the run.
	if res.Status != agent.StatusCompleted {
		t.Fatalf("status = %v, want completed", res.Status)
	}
}
