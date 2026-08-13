// Package execd is opencraft's remote execution transport: the
// server-side JSON-RPC protocol wraps a sandbox.Runner, and the
// client-side RemoteRunner implements sandbox.Runner so callers never
// see the wire.
package execd

import (
	"context"
	"sync"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/sandbox"

	"github.com/rs/xid"
)

// RemoteRunner is a sandbox.Runner backed by a forked execd child.
// Close terminates the child process.
type RemoteRunner struct {
	client *Client
	stop   func()
	caps   sandbox.Capabilities
}

// NewRemoteRunner queries the child's capabilities and returns a
// runner that proxies every operation over JSON-RPC. stop terminates
// the child and is invoked by Close.
func NewRemoteRunner(client *Client, stop func()) (*RemoteRunner, error) {
	ctx := context.Background()
	info, err := client.EnvironmentInfo(ctx)
	if err != nil {
		if stop != nil {
			stop()
		}
		return nil, err
	}
	features := sandbox.SessionFeatures{}
	for _, cap := range info.Capabilities {
		switch Capability(cap) {
		case CapPTY:
			features.TTY = true
		case CapSignal:
			features.Signal = true
		}
	}
	return &RemoteRunner{
		client: client,
		stop:   stop,
		caps: sandbox.Capabilities{
			Features: features,
		},
	}, nil
}

var _ sandbox.Runner = (*RemoteRunner)(nil)

// Capabilities reports the child server's session features.
func (r *RemoteRunner) Capabilities() sandbox.Capabilities { return r.caps }

// Start starts one session in the child.
func (r *RemoteRunner) Start(
	ctx context.Context,
	spec sandbox.SessionSpec,
) (sandbox.Session, error) {
	if spec.ID == "" {
		spec.ID = xid.New().String()
	}
	opts := spec.Opts
	resp, err := r.client.Start(ctx, ExecParams{
		ProcessID: spec.ID,
		Argv:      spec.Argv,
		Cwd:       opts.WorkDir,
		Env:       opts.Env.Inject,
		TTY:       spec.TTY,
		Timeout:   opts.Timeout,
		Rows:      spec.Rows,
		Cols:      spec.Cols,
		Sandbox:   &opts,
	})
	if err != nil {
		return nil, err
	}
	return &remoteSession{
		client: r.client,
		id:     resp.ProcessID,
		pid:    resp.PID,
		caps: sandbox.SessionCapabilities{
			TTY:    spec.TTY && r.caps.Features.TTY,
			Signal: r.caps.Features.Signal,
		},
	}, nil
}

// List is not part of the execd wire protocol yet.
func (r *RemoteRunner) List(context.Context) ([]sandbox.SessionInfo, error) {
	return nil, errdefs.NotAvailablef("execd: session list is not supported")
}

// Terminate stops a session in the child.
func (r *RemoteRunner) Terminate(ctx context.Context, id string) error {
	_, err := r.client.Terminate(ctx, TerminateParams{ProcessID: id})
	return err
}

// Close shuts the client down and terminates the child.
func (r *RemoteRunner) Close() error {
	if r.client != nil {
		_ = r.client.Close()
	}
	if r.stop != nil {
		r.stop()
	}
	return nil
}

// remoteSession adapts the execd protocol to sandbox.Session.
type remoteSession struct {
	client *Client
	id     string
	pid    int
	caps   sandbox.SessionCapabilities

	mu      sync.Mutex
	lastSeq int64
	exited  bool
	exit    *sandbox.SessionExit
}

var _ sandbox.Session = (*remoteSession)(nil)

func (s *remoteSession) ID() string  { return s.id }
func (s *remoteSession) PID() int    { return s.pid }
func (s *remoteSession) Capabilities() sandbox.SessionCapabilities {
	return s.caps
}

func (s *remoteSession) Read(
	ctx context.Context,
	afterSeq int64,
	maxBytes int,
) (sandbox.SessionOutput, error) {
	resp, err := s.client.Read(ctx, ReadParams{
		ProcessID: s.id,
		AfterSeq:  &afterSeq,
		MaxBytes:  &maxBytes,
	})
	if err != nil {
		return sandbox.SessionOutput{}, err
	}
	chunks := make([]sandbox.OutputChunk, 0, len(resp.Chunks))
	for _, ch := range resp.Chunks {
		chunks = append(chunks, sandbox.OutputChunk{
			Seq:    ch.Seq,
			Stream: sandboxStream(ch.Stream),
			Data:   ch.Data,
		})
	}
	out := sandbox.SessionOutput{NextSeq: resp.NextSeq, Chunks: chunks, EOF: resp.EOF}
	s.mu.Lock()
	if resp.Exited && !s.exited {
		s.exited = true
		exit := sandbox.SessionExit{
			Code:   0,
			Reason: sandbox.SessionExited,
		}
		if resp.ExitCode != nil {
			exit.Code = int(*resp.ExitCode)
		}
		if resp.Reason != "" {
			exit.Reason = sessionReason(resp.Reason)
		}
		s.exit = &exit
	}
	s.mu.Unlock()
	return out, nil
}

func (s *remoteSession) Write(ctx context.Context, data []byte) error {
	_, err := s.client.Write(ctx, WriteParams{
		ProcessID: s.id,
		Chunk:     data,
		WriteID:   xid.New().String(),
	})
	return err
}

func (s *remoteSession) CloseInput() error {
	return s.client.CloseInput(context.Background(), CloseInputParams{
		ProcessID: s.id,
	})
}

func (s *remoteSession) Resize(ctx context.Context, rows, cols int) error {
	return s.client.Resize(ctx, ResizeParams{
		ProcessID: s.id,
		Rows:      rows,
		Cols:      cols,
	})
}

func (s *remoteSession) Signal(ctx context.Context, _ sandbox.SessionSignal) error {
	return s.client.Signal(ctx, SignalParams{
		ProcessID: s.id,
		Signal:    string(SignalInterrupt),
	})
}

func (s *remoteSession) Terminate(ctx context.Context) error {
	_, err := s.client.Terminate(ctx, TerminateParams{ProcessID: s.id})
	return err
}

func (s *remoteSession) Wait(ctx context.Context) (sandbox.SessionExit, error) {
	for {
		s.mu.Lock()
		exited, exit := s.exited, s.exit
		after := s.lastSeq
		s.mu.Unlock()
		if exited {
			if exit != nil {
				return *exit, nil
			}
			return sandbox.SessionExit{
				Code: 0, Reason: sandbox.SessionExited,
			}, nil
		}
		out, err := s.Read(ctx, after, 1)
		if err != nil {
			return sandbox.SessionExit{}, err
		}
		s.mu.Lock()
		s.lastSeq = out.NextSeq
		s.mu.Unlock()
	}
}

// Watch is not implemented on the client yet; output remains pullable
// through Read.
func (s *remoteSession) Watch(context.Context) (sandbox.SessionWatcher, error) {
	return nil, errdefs.NotAvailablef(
		"execd: watch is not implemented on the remote client")
}

func (s *remoteSession) Close() error {
	_ = s.Terminate(context.Background())
	return nil
}

func sandboxStream(stream string) sandbox.SessionStream {
	switch stream {
	case "stderr":
		return sandbox.SessionStreamStderr
	case "tty":
		return sandbox.SessionStreamTTY
	default:
		return sandbox.SessionStreamStdout
	}
}

func sessionReason(reason string) sandbox.SessionExitReason {
	switch reason {
	case ReasonSignaled:
		return sandbox.SessionSignaled
	case ReasonTerminated:
		return sandbox.SessionTerminated
	default:
		return sandbox.SessionExited
	}
}
