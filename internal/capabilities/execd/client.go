package execd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

// Method names are used for typed error messages only; the wire carries
// a protobuf oneof instead of a string method field.
const (
	methodHello       = "hello"
	methodBind        = "bind"
	methodUnbind      = "unbind"
	methodStart       = "start"
	methodRead        = "read"
	methodWrite       = "write"
	methodCloseInput  = "close_input"
	methodSignal      = "signal"
	methodResize      = "resize"
	methodTerminate   = "terminate"
	methodRelease     = "release"
	methodWait        = "wait"
	methodPing        = "ping"
	methodDiagnostics = "diagnostics"
)

// Build is the informational build string exchanged in Hello. The
// protocol version is what gates compatibility.
var Build = "dev"

// errClientClosed reports a call on a closed client.
var errClientClosed = errors.New("execd: client closed")

// Client is the parent side of the execd protocol: synchronous requests
// plus notification delivery.
type Client struct {
	conn   io.ReadWriteCloser
	reader *bufio.Reader

	// writeMu serialises frames on the connection. The read loop never
	// takes it, so a blocked write cannot stall response dispatch.
	writeMu sync.Mutex

	// mu guards request bookkeeping only; it is never held across a
	// transport write.
	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan *Frame
	notify  func(*Notification)
	hello   *HelloOk

	closeOnce sync.Once
	done      chan struct{}
	readDone  chan struct{}
}

// Dial performs the Hello handshake over conn and starts the read loop.
func Dial(ctx context.Context, conn io.ReadWriteCloser) (*Client, error) {
	c := &Client{
		conn:     conn,
		reader:   bufio.NewReaderSize(conn, 64<<10),
		pending:  make(map[uint64]chan *Frame),
		done:     make(chan struct{}),
		readDone: make(chan struct{}),
	}
	go c.readLoop()
	hello, err := c.Hello(ctx)
	if err != nil {
		closeLog(ctx, "execd: close connection after handshake failure", conn)
		return nil, err
	}
	if hello.GetProtocolVersion() != ProtocolVersion {
		closeLog(ctx, "execd: close connection after version mismatch", conn)
		return nil, &RPCError{
			Method: methodHello,
			Code:   CodeVersionMismatch,
			Message: fmt.Sprintf("child protocol %d, host protocol %d",
				hello.GetProtocolVersion(), ProtocolVersion),
		}
	}
	c.mu.Lock()
	c.hello = hello
	c.mu.Unlock()
	return c, nil
}

// HelloInfo returns the child's handshake response.
func (c *Client) HelloInfo() *HelloOk {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hello == nil {
		return &HelloOk{}
	}
	return c.hello
}

// SetNotificationHandler registers the receiver for child
// notifications. Only one handler is supported.
func (c *Client) SetNotificationHandler(fn func(*Notification)) {
	c.mu.Lock()
	c.notify = fn
	c.mu.Unlock()
}

// Close closes the connection; the read loop exits and all pending
// calls fail.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.done)
		c.mu.Lock()
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		c.mu.Unlock()
		err = c.conn.Close()
	})
	return err
}

// WaitReadLoop blocks until the read loop has returned or ctx expires.
func (c *Client) WaitReadLoop(ctx context.Context) error {
	select {
	case <-c.readDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) register() (uint64, chan *Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	ch := make(chan *Frame, 1)
	c.pending[id] = ch
	return id, ch
}

func (c *Client) unregister(id uint64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Client) writeFrame(frame *Frame) error {
	raw, err := encodeFrame(frame)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for len(raw) > 0 {
		n, err := c.conn.Write(raw)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		raw = raw[n:]
	}
	return nil
}

// sendCancel tells the child to cancel one in-flight request. It is
// best-effort: the caller's context is already done, so the write gets
// its own short budget and never blocks the caller.
func (c *Client) sendCancel(id uint64) {
	if c.closing() {
		return
	}
	go func() {
		done := make(chan struct{})
		go func() {
			telemetry.WarnErr(context.Background(),
				"execd: send cancel notification failed", c.writeFrame(&Frame{
					Body: &Frame_Notification{Notification: &Notification{
						Body: &Notification_Cancel{Cancel: &Cancel{RequestId: id}},
					}},
				}))
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
	}()
}

func (c *Client) closing() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *Client) call(
	ctx context.Context,
	method string,
	req *Request,
) (*Response, error) {
	id, ch := c.register()
	frame := &Frame{
		Id:         id,
		DeadlineMs: deadlineMillis(ctx),
		Body:       &Frame_Request{Request: req},
	}
	if err := c.writeFrame(frame); err != nil {
		c.unregister(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.unregister(id)
		c.sendCancel(id)
		return nil, ctx.Err()
	case <-c.done:
		c.unregister(id)
		return nil, errClientClosed
	case resp, ok := <-ch:
		if !ok {
			return nil, errClientClosed
		}
		response := resp.GetResponse()
		if response == nil {
			return nil, unexpectedResponse(method)
		}
		if wireErr := response.GetError(); wireErr != nil {
			return nil, errorFromResponse(method, wireErr)
		}
		return response, nil
	}
}

func unexpectedResponse(method string) error {
	return &RPCError{
		Method:  method,
		Code:    CodeInternal,
		Message: "unexpected response type",
	}
}

// Typed calls -------------------------------------------------------------

func (c *Client) Hello(ctx context.Context) (*HelloOk, error) {
	resp, err := c.call(ctx, methodHello, &Request{
		Method: &Request_Hello{Hello: &Hello{
			ProtocolVersion: ProtocolVersion,
			Build:           Build,
			Client:          "opencraft",
		}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetHelloOk()
	if ok == nil {
		return nil, unexpectedResponse(methodHello)
	}
	return ok, nil
}

func (c *Client) Bind(
	ctx context.Context,
	workdir string,
	policy *SandboxPolicy,
) (*BindOk, error) {
	resp, err := c.call(ctx, methodBind, &Request{
		Method: &Request_Bind{Bind: &Bind{Workdir: workdir, Policy: policy}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetBindOk()
	if ok == nil {
		return nil, unexpectedResponse(methodBind)
	}
	return ok, nil
}

func (c *Client) Unbind(ctx context.Context) error {
	resp, err := c.call(ctx, methodUnbind, &Request{
		Method: &Request_Unbind{Unbind: &Unbind{}},
	})
	if err != nil {
		return err
	}
	if resp.GetAck() == nil {
		return unexpectedResponse(methodUnbind)
	}
	return nil
}

func (c *Client) Start(ctx context.Context, params *Start) (*StartOk, error) {
	resp, err := c.call(ctx, methodStart, &Request{
		Method: &Request_Start{Start: params},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetStartOk()
	if ok == nil {
		return nil, unexpectedResponse(methodStart)
	}
	return ok, nil
}

func (c *Client) Read(ctx context.Context, params *Read) (*ReadOk, error) {
	resp, err := c.call(ctx, methodRead, &Request{
		Method: &Request_Read{Read: params},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetReadOk()
	if ok == nil {
		return nil, unexpectedResponse(methodRead)
	}
	return ok, nil
}

func (c *Client) Write(ctx context.Context, params *Write) (*Ack, error) {
	resp, err := c.call(ctx, methodWrite, &Request{
		Method: &Request_Write{Write: params},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetAck()
	if ok == nil {
		return nil, unexpectedResponse(methodWrite)
	}
	return ok, nil
}

func (c *Client) CloseInput(ctx context.Context, processID string) error {
	resp, err := c.call(ctx, methodCloseInput, &Request{
		Method: &Request_CloseInput{CloseInput: &CloseInput{ProcessId: processID}},
	})
	if err != nil {
		return err
	}
	if resp.GetAck() == nil {
		return unexpectedResponse(methodCloseInput)
	}
	return nil
}

func (c *Client) Signal(ctx context.Context, processID string, signal uint32) error {
	resp, err := c.call(ctx, methodSignal, &Request{
		Method: &Request_Signal{Signal: &Signal{
			ProcessId: processID,
			Signal:    signal,
		}},
	})
	if err != nil {
		return err
	}
	if resp.GetAck() == nil {
		return unexpectedResponse(methodSignal)
	}
	return nil
}

func (c *Client) Resize(ctx context.Context, processID string, rows, cols int32) error {
	resp, err := c.call(ctx, methodResize, &Request{
		Method: &Request_Resize{Resize: &Resize{
			ProcessId: processID,
			Rows:      rows,
			Cols:      cols,
		}},
	})
	if err != nil {
		return err
	}
	if resp.GetAck() == nil {
		return unexpectedResponse(methodResize)
	}
	return nil
}

func (c *Client) Terminate(
	ctx context.Context,
	processID string,
	force bool,
) (*TerminateOk, error) {
	resp, err := c.call(ctx, methodTerminate, &Request{
		Method: &Request_Terminate{Terminate: &Terminate{
			ProcessId: processID,
			Force:     force,
		}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetTerminateOk()
	if ok == nil {
		return nil, unexpectedResponse(methodTerminate)
	}
	return ok, nil
}

func (c *Client) Release(ctx context.Context, processID string) (*ReleaseOk, error) {
	resp, err := c.call(ctx, methodRelease, &Request{
		Method: &Request_Release{Release: &Release{ProcessId: processID}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetReleaseOk()
	if ok == nil {
		return nil, unexpectedResponse(methodRelease)
	}
	return ok, nil
}

func (c *Client) Wait(ctx context.Context, processID string) (*WaitOk, error) {
	resp, err := c.call(ctx, methodWait, &Request{
		Method: &Request_Wait{Wait: &Wait{ProcessId: processID}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetWaitOk()
	if ok == nil {
		return nil, unexpectedResponse(methodWait)
	}
	return ok, nil
}

func (c *Client) Ping(ctx context.Context) (*PingOk, error) {
	resp, err := c.call(ctx, methodPing, &Request{
		Method: &Request_Ping{Ping: &Ping{}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetPingOk()
	if ok == nil {
		return nil, unexpectedResponse(methodPing)
	}
	return ok, nil
}

func (c *Client) Diagnostics(ctx context.Context) (*DiagnosticsOk, error) {
	resp, err := c.call(ctx, methodDiagnostics, &Request{
		Method: &Request_Diagnostics{Diagnostics: &Diagnostics{}},
	})
	if err != nil {
		return nil, err
	}
	ok := resp.GetDiagnosticsOk()
	if ok == nil {
		return nil, unexpectedResponse(methodDiagnostics)
	}
	return ok, nil
}

// readLoop dispatches responses by id and notifications to the handler.
func (c *Client) readLoop() {
	defer close(c.readDone)
	for {
		frame, err := decodeFrame(c.reader)
		if err != nil {
			if !c.closing() && !connectionClosed(err) {
				telemetry.WarnErr(context.Background(),
					"execd: decode frame failed", err)
			}
			telemetry.WarnErr(context.Background(),
				"execd: close client after decode failure", c.Close())
			return
		}
		if notification := frame.GetNotification(); notification != nil {
			c.mu.Lock()
			handler := c.notify
			c.mu.Unlock()
			if handler != nil {
				handler(notification)
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[frame.GetId()]
		if ok {
			delete(c.pending, frame.GetId())
		}
		c.mu.Unlock()
		if ok {
			ch <- frame
		}
	}
}

// connectionClosed reports whether err is the ordinary end of a
// connection — our own close, or the peer hanging up.
func connectionClosed(err error) bool {
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.EPIPE)
}
