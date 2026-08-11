package execd

import (
	"context"
	"strings"
	"sync"

	"github.com/GizClaw/flowcraft/sdk/errdefs"

	"github.com/rs/xid"
)

// RemoteEnvironment runs processes through an execd client. Its
// capabilities mirror the protocol (sessions, pty, signals); one-shot
// Exec and filesystems are not implemented yet.
type RemoteEnvironment struct {
	id     string
	client *Client
	caps   []Capability
}

// NewRemoteEnvironment wraps a connected execd client.
func NewRemoteEnvironment(id string, client *Client) *RemoteEnvironment {
	return &RemoteEnvironment{
		id:     id,
		client: client,
		caps:   []Capability{CapExec, CapSession, CapPTY, CapSignal},
	}
}

var _ Environment = (*RemoteEnvironment)(nil)

func (e *RemoteEnvironment) ID() string { return e.id }

func (e *RemoteEnvironment) Capabilities() []Capability { return e.caps }

// Exec runs a one-shot command through a session: start, drain output,
// wait for exit, collect the result.
func (e *RemoteEnvironment) Exec(
	ctx context.Context,
	req Request,
) (Result, error) {
	spec := Spec{
		ID:    xid.New().String(),
		Argv:  req.Argv,
		Input: req,
	}
	proc, err := e.Start(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	defer proc.Close()

	var stdout, stderr strings.Builder
	after := int64(0)
	for {
		out, err := proc.Read(ctx, after, 1<<20)
		if err != nil {
			return Result{}, err
		}
		for _, ch := range out.Chunks {
			if ch.Stream == Stdout {
				stdout.Write(ch.Data)
			} else {
				stderr.Write(ch.Data)
			}
		}
		after = out.NextSeq
		if out.EOF {
			break
		}
	}
	exit, err := proc.Wait(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{
		ExitCode: exit.Code,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func (e *RemoteEnvironment) Start(
	ctx context.Context,
	spec Spec,
) (Process, error) {
	opts := toSandboxExecOptions(spec.Input)
	params := ExecParams{
		ProcessID: spec.ID,
		Argv:      spec.Argv,
		TTY:       spec.TTY,
		Rows:      spec.Rows,
		Cols:      spec.Cols,
		Sandbox:   &opts,
	}
	if spec.Input.WorkDir != "" {
		params.Cwd = spec.Input.WorkDir
	}
	resp, err := e.client.Start(ctx, params)
	if err != nil {
		return nil, err
	}
	return &remoteProcess{client: e.client, id: resp.ProcessID}, nil
}

// remoteProcess adapts one remote session to the Process contract.
type remoteProcess struct {
	client *Client
	id     string

	mu      sync.Mutex
	lastSeq int64
	exited  bool
	exit    *Exit
}

var _ Process = (*remoteProcess)(nil)
var _ Signaler = (*remoteProcess)(nil)
var _ Resizer = (*remoteProcess)(nil)

func (p *remoteProcess) ID() string { return p.id }

func (p *remoteProcess) Read(
	ctx context.Context,
	afterSeq int64,
	maxBytes int,
) (Output, error) {
	resp, err := p.client.Read(ctx, ReadParams{
		ProcessID: p.id,
		AfterSeq:  &afterSeq,
		MaxBytes:  &maxBytes,
	})
	if err != nil {
		return Output{}, err
	}
	chunks := make([]Chunk, 0, len(resp.Chunks))
	for _, ch := range resp.Chunks {
		chunks = append(chunks, Chunk{
			Seq: ch.Seq, Stream: Stream(ch.Stream), Data: ch.Data,
		})
	}
	out := Output{NextSeq: resp.NextSeq, Chunks: chunks, EOF: resp.EOF}
	p.mu.Lock()
	if resp.Exited && !p.exited {
		p.exited = true
		exit := Exit{Code: 0, Reason: ExitReasonExited}
		if resp.ExitCode != nil {
			exit.Code = int(*resp.ExitCode)
		}
		p.exit = &exit
	}
	p.mu.Unlock()
	return out, nil
}

func (p *remoteProcess) Write(ctx context.Context, data []byte) error {
	_, err := p.client.Write(ctx, WriteParams{
		ProcessID: p.id,
		Chunk:     data,
		WriteID:   xid.New().String(),
	})
	return err
}

func (p *remoteProcess) Terminate(ctx context.Context) error {
	_, err := p.client.Terminate(ctx, TerminateParams{ProcessID: p.id})
	return err
}

// Wait blocks until the process exits, draining output through Read.
func (p *remoteProcess) Wait(ctx context.Context) (Exit, error) {
	for {
		p.mu.Lock()
		exited, exit := p.exited, p.exit
		after := p.lastSeq
		p.mu.Unlock()
		if exited {
			if exit != nil {
				return *exit, nil
			}
			return Exit{Code: 0, Reason: ExitReasonExited}, nil
		}
		out, err := p.Read(ctx, after, 1)
		if err != nil {
			return Exit{}, err
		}
		p.mu.Lock()
		p.lastSeq = out.NextSeq
		p.mu.Unlock()
	}
}

func (p *remoteProcess) Close() error {
	// Processes are owned by the execd connection; the server
	// terminates them when the connection closes.
	return nil
}

func (p *remoteProcess) Signal(ctx context.Context, sig Signal) error {
	if sig != SignalInterrupt {
		return errdefs.NotAvailablef(
			"execd: unsupported signal %q", sig)
	}
	return p.client.Signal(ctx, SignalParams{
		ProcessID: p.id,
		Signal:    string(SignalInterrupt),
	})
}

func (p *remoteProcess) Resize(ctx context.Context, rows, cols int) error {
	return p.client.Resize(ctx, ResizeParams{
		ProcessID: p.id,
		Rows:      rows,
		Cols:      cols,
	})
}
