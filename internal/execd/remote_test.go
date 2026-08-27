package execd

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

func TestRemoteRunnerStartRead(t *testing.T) {
	client, _ := testPair(t)
	runner, err := NewRemoteRunner(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	proc, err := runner.Start(ctx, sandbox.SessionSpec{
		ID:   "rp1",
		Argv: []string{"/bin/sh", "-c", "echo remote-ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if proc.ID() != "rp1" {
		t.Errorf("id = %q", proc.ID())
	}

	out, err := proc.Read(ctx, 0, 4096)
	if err != nil {
		t.Fatal(err)
	}
	var all []byte
	for _, ch := range out.Chunks {
		all = append(all, ch.Data...)
	}
	if !strings.Contains(string(all), "remote-ok") {
		t.Errorf("output = %q", all)
	}
}

func TestRemoteRunnerExec(t *testing.T) {
	client, _ := testPair(t)
	runner, err := NewRemoteRunner(client, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := sandbox.Exec(context.Background(), runner,
		"/bin/sh", []string{"-c", "echo exec-ok; echo err 1>&2"},
		sandbox.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 ||
		!strings.Contains(result.Stdout, "exec-ok") ||
		!strings.Contains(result.Stderr, "err") {
		t.Errorf("result = %+v", result)
	}
}
