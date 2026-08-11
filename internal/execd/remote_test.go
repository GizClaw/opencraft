package execd

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRemoteEnvironmentStartRead verifies the environment abstraction:
// a RemoteEnvironment over a live server drives sessions through the
// protocol and surfaces output to consumers.
func TestRemoteEnvironmentStartRead(t *testing.T) {
	client, _ := testPair(t)
	env := NewRemoteEnvironment("remote", client)
	ctx := context.Background()

	if !Has(env, CapSession) {
		t.Fatal("remote should declare sessions")
	}
	proc, err := env.Start(ctx, Spec{
		ID:   "rp1",
		Argv: []string{"/bin/sh", "-c", "echo remote-ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proc.ID() != "rp1" {
		t.Errorf("id = %q", proc.ID())
	}

	var all []byte
	after := int64(0)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("timed out; output %q", all)
		}
		out, err := proc.Read(ctx, after, 4096)
		if err != nil {
			t.Fatal(err)
		}
		for _, ch := range out.Chunks {
			all = append(all, ch.Data...)
		}
		after = out.NextSeq
		if out.EOF {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(string(all), "remote-ok") {
		t.Errorf("output = %q", all)
	}
}

func TestRemoteEnvironmentWait(t *testing.T) {
	client, _ := testPair(t)
	env := NewRemoteEnvironment("remote", client)
	ctx := context.Background()

	proc, err := env.Start(ctx, Spec{
		ID:   "rp2",
		Argv: []string{"/bin/sh", "-c", "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	exit, err := proc.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if exit.Code != 0 {
		t.Errorf("exit code = %d", exit.Code)
	}
}

func TestRemoteEnvironmentExec(t *testing.T) {
	client, _ := testPair(t)
	env := NewRemoteEnvironment("remote", client)
	if !Has(env, CapExec) {
		t.Fatal("remote should declare one-shot exec")
	}
	result, err := env.Exec(context.Background(), Request{
		Argv: []string{"/bin/sh", "-c", "echo exec-ok; echo err 1>&2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "exec-ok") ||
		!strings.Contains(result.Stderr, "err") {
		t.Errorf("result = %+v", result)
	}
}

func TestEnvironmentManager(t *testing.T) {
	client, _ := testPair(t)
	mgr := NewEnvironmentManager()
	if err := mgr.Register(NewRemoteEnvironment("remote", client)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Register(NewRemoteEnvironment("remote", client)); err == nil {
		t.Error("duplicate register accepted")
	}
	if err := mgr.Register(NewRemoteEnvironment("local", client)); err == nil {
		t.Error("reserved id accepted")
	}
	if _, ok := mgr.Get("remote"); !ok {
		t.Error("remote not found")
	}
	if _, ok := mgr.Get("nope"); ok {
		t.Error("unknown environment found")
	}
	if names := mgr.Names(); len(names) != 1 {
		t.Errorf("names = %v", names)
	}
}
