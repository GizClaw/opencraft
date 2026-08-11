package execd

import (
	"context"

	"github.com/GizClaw/flowcraft/sdk/errdefs"
	"github.com/GizClaw/flowcraft/sdk/sandbox"
)

// LocalEnvironment runs processes in-process through a sandbox.Runner
// (and its optional ProcessManager). It is the default environment.
type LocalEnvironment struct {
	id     string
	runner sandbox.Runner
	pm     sandbox.ProcessManager
	caps   []Capability
}

// NewLocalEnvironment creates the local environment over runner.
func NewLocalEnvironment(runner sandbox.Runner) *LocalEnvironment {
	caps := []Capability{CapExec}
	pm := sandbox.ProcessManagerOf(runner)
	if pm != nil {
		caps = append(caps, CapSession, CapSignal, CapPTY)
	}
	return &LocalEnvironment{
		id: "local", runner: runner, pm: pm, caps: caps,
	}
}

var _ Environment = (*LocalEnvironment)(nil)

func (e *LocalEnvironment) ID() string { return e.id }

func (e *LocalEnvironment) Capabilities() []Capability { return e.caps }

func (e *LocalEnvironment) Exec(
	ctx context.Context,
	req Request,
) (Result, error) {
	if len(req.Argv) == 0 {
		return Result{}, errdefs.Validationf("local: argv is required")
	}
	res, err := e.runner.Exec(ctx, req.Argv[0], req.Argv[1:], sandbox.ExecOptions{
		WorkDir:   req.WorkDir,
		Stdin:     req.Stdin,
		Env:       req.Env,
		Net:       req.Net,
		Resources: req.Resources,
		Timeout:   req.Timeout,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}, nil
}

func (e *LocalEnvironment) Start(
	ctx context.Context,
	spec Spec,
) (Process, error) {
	if e.pm == nil {
		return nil, errdefs.NotAvailablef(
			"local: runner has no process manager")
	}
	proc, err := e.pm.Start(ctx, sandbox.ProcessSpec{
		ID:   spec.ID,
		Argv: spec.Argv,
		TTY:  spec.TTY,
		Rows: spec.Rows,
		Cols: spec.Cols,
		Opts: sandbox.ExecOptions{
			WorkDir:   spec.Input.WorkDir,
			Env:       spec.Input.Env,
			Net:       spec.Input.Net,
			Resources: spec.Input.Resources,
			Timeout:   spec.Input.Timeout,
		},
	})
	if err != nil {
		return nil, err
	}
	return &localProcess{proc: proc}, nil
}

// localProcess adapts a sandbox.Process to the execd Process
// contract.
type localProcess struct {
	proc sandbox.Process
}

var _ Process = (*localProcess)(nil)
var _ Signaler = (*localProcess)(nil)
var _ Resizer = (*localProcess)(nil)

func (p *localProcess) ID() string { return p.proc.ID() }

func (p *localProcess) Read(
	ctx context.Context,
	afterSeq int64,
	maxBytes int,
) (Output, error) {
	out, err := p.proc.Read(ctx, afterSeq, maxBytes)
	if err != nil {
		return Output{}, err
	}
	chunks := make([]Chunk, 0, len(out.Chunks))
	for _, ch := range out.Chunks {
		chunks = append(chunks, Chunk{
			Seq: ch.Seq, Stream: Stream(ch.Stream.String()), Data: ch.Data,
		})
	}
	return Output{NextSeq: out.NextSeq, Chunks: chunks, EOF: out.EOF}, nil
}

func (p *localProcess) Write(ctx context.Context, data []byte) error {
	return p.proc.Write(ctx, data)
}

func (p *localProcess) Terminate(ctx context.Context) error {
	return p.proc.Terminate(ctx)
}

func (p *localProcess) Wait(ctx context.Context) (Exit, error) {
	exit, err := p.proc.Wait(ctx)
	if err != nil {
		return Exit{}, err
	}
	return Exit{Code: exit.Code, Reason: exitReason(exit.Reason)}, nil
}

func (p *localProcess) Close() error { return p.proc.Close() }

func (p *localProcess) Signal(ctx context.Context, sig Signal) error {
	if sig != SignalInterrupt {
		return errdefs.NotAvailablef(
			"local: unsupported signal %q", sig)
	}
	s, ok := sandbox.ProcessSignalerOf(p.proc)
	if !ok {
		return errdefs.NotAvailablef("local: process has no signal support")
	}
	return s.Signal(ctx, sandbox.ProcessSignalInterrupt)
}

func (p *localProcess) Resize(ctx context.Context, rows, cols int) error {
	return p.proc.Resize(ctx, rows, cols)
}

func exitReason(r sandbox.ProcessExitReason) ExitReason {
	switch r {
	case sandbox.ProcessExited:
		return ExitReasonExited
	case sandbox.ProcessSignaled:
		return ExitReasonSignaled
	case sandbox.ProcessTerminated:
		return ExitReasonTerminated
	default:
		return ExitReasonUnknown
	}
}
