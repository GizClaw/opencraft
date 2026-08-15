package execd

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/sandbox/local"
)

func testPair(t *testing.T) (*Client, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	backend := local.New(root)

	serverConn, clientConn := net.Pipe()
	go func() {
		_ = New(backend, serverConn, serverConn).Serve(ctx)
	}()

	client, err := Dial(ctx, clientConn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		cancel()
	})
	return client, cancel
}

func TestServeEnvironment(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.Shell != "/bin/sh" || !strings.Contains(strings.Join(info.Capabilities, ","), "signal") {
		t.Errorf("info = %+v", info)
	}
}

func TestServeStartRead(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	resp, err := client.Start(ctx, ExecParams{
		ProcessID: "p1",
		Argv:      []string{"/bin/sh", "-c", "echo hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ProcessID != "p1" {
		t.Errorf("processId = %q", resp.ProcessID)
	}

	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output so far %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "p1",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "hello") {
		t.Errorf("output = %q", all)
	}
}

func TestServeStartReadStderr(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p2",
		Argv:      []string{"/bin/sh", "-c", "echo err 1>&2"},
	}); err != nil {
		t.Fatal(err)
	}

	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output so far %q", all)
		}
		read, err := client.Read(ctx, ReadParams{
			ProcessID: "p2",
			AfterSeq:  &after,
			MaxBytes:  intPtr(4096),
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range read.Chunks {
			if ch.Stream != StreamStderr {
				t.Errorf("stream = %q, want stderr", ch.Stream)
			}
			all = append(all, ch.Data...)
		}
		after = read.NextSeq
		if read.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "err") {
		t.Errorf("output = %q", all)
	}
}

func TestServeTerminate(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p3",
		Argv:      []string{"/bin/sh", "-c", "sleep 100"},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Terminate(ctx, TerminateParams{ProcessID: "p3"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Running {
		t.Error("process still running after terminate")
	}
}

func TestServeWriteIdempotent(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "p4",
		Argv:      []string{"/bin/cat"},
		PipeStdin: true,
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	first, err := client.Write(ctx, WriteParams{
		ProcessID: "p4", Chunk: []byte("x"), WriteID: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != WriteAccepted {
		t.Errorf("first write = %q", first.Status)
	}
	second, err := client.Write(ctx, WriteParams{
		ProcessID: "p4", Chunk: []byte("x"), WriteID: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != WriteAccepted {
		t.Errorf("duplicate write = %q", second.Status)
	}
}

func TestServeResize(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "rz",
		Argv:      []string{"/bin/sh"},
		TTY:       true,
		Rows:      24,
		Cols:      80,
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := client.Resize(ctx, ResizeParams{
		ProcessID: "rz",
		Rows:      40,
		Cols:      120,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestServeEventNotifications(t *testing.T) {
	client, _ := testPair(t)
	ctx := context.Background()

	var mu sync.Mutex
	var outputs []string
	exited := make(chan struct{}, 1)
	closed := make(chan struct{}, 1)
	client.OnNotification(MethodProcessOutput, func(raw json.RawMessage) {
		var n OutputNotification
		if err := json.Unmarshal(raw, &n); err != nil {
			return
		}
		mu.Lock()
		outputs = append(outputs, string(n.Data))
		mu.Unlock()
	})
	client.OnNotification(MethodProcessExited, func(raw json.RawMessage) {
		exited <- struct{}{}
	})
	client.OnNotification(MethodProcessClosed, func(raw json.RawMessage) {
		closed <- struct{}{}
	})

	if _, err := client.Start(ctx, ExecParams{
		ProcessID: "evt",
		Argv:      []string{"/bin/sh", "-c", "echo event-ok"},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process/exited notification")
	}
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for process/closed notification")
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(outputs, "")
	if !strings.Contains(joined, "event-ok") {
		t.Errorf("output notifications = %q", joined)
	}
}

func intPtr(v int) *int { return &v }
