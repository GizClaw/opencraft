package execd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Client is the execd JSON-RPC client: synchronous requests plus
// notification delivery to registered handlers.
type Client struct {
	conn      io.ReadWriteCloser
	mu        sync.Mutex
	nextID    int64
	pending   map[int64]chan Response
	handlers  map[string]func(json.RawMessage)
	closeOnce sync.Once
	done      chan struct{}
}

// Dial performs the initialize handshake over conn and starts the read
// loop.
func Dial(ctx context.Context, conn io.ReadWriteCloser) (*Client, error) {
	c := &Client{
		conn:     conn,
		pending:  make(map[int64]chan Response),
		handlers: make(map[string]func(json.RawMessage)),
		done:     make(chan struct{}),
	}
	go c.readLoop()
	var init InitializeResponse
	if err := c.call(ctx, MethodInitialize, InitializeParams{ClientName: "opencraft"}, &init); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// OnNotification registers a handler for one notification method.
func (c *Client) OnNotification(method string, fn func(json.RawMessage)) {
	c.mu.Lock()
	c.handlers[method] = fn
	c.mu.Unlock()
}

func (c *Client) Start(ctx context.Context, params ExecParams) (*ExecResponse, error) {
	var out ExecResponse
	if err := c.call(ctx, MethodProcessStart, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Read(ctx context.Context, params ReadParams) (*ReadResponse, error) {
	var out ReadResponse
	if err := c.call(ctx, MethodProcessRead, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Write(ctx context.Context, params WriteParams) (*WriteResponse, error) {
	var out WriteResponse
	if err := c.call(ctx, MethodProcessWrite, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) Signal(ctx context.Context, params SignalParams) error {
	var out map[string]bool
	return c.call(ctx, MethodProcessSignal, params, &out)
}

func (c *Client) Resize(ctx context.Context, params ResizeParams) error {
	var out map[string]bool
	return c.call(ctx, MethodProcessResize, params, &out)
}

func (c *Client) Terminate(ctx context.Context, params TerminateParams) (*TerminateResponse, error) {
	var out TerminateResponse
	if err := c.call(ctx, MethodProcessTerminate, params, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) EnvironmentInfo(ctx context.Context) (*EnvironmentInfoResponse, error) {
	var out EnvironmentInfoResponse
	if err := c.call(ctx, MethodEnvironmentInfo, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) EnvironmentStatus(ctx context.Context) (*EnvironmentStatusResponse, error) {
	var out EnvironmentStatusResponse
	if err := c.call(ctx, MethodEnvironmentStatus, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
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

func (c *Client) call(
	ctx context.Context,
	method string,
	params any,
	out any,
) error {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan Response, 1)
	c.pending[id] = ch
	raw, err := json.Marshal(params)
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	req := RPCRequest{JSONRPC: "2.0", ID: &id, Method: method, Params: raw}
	reqRaw, err := json.Marshal(req)
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	_, err = c.conn.Write(append(reqRaw, '\n'))
	c.mu.Unlock()
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return fmt.Errorf("execd: connection closed")
		}
		if resp.Error != nil {
			return fmt.Errorf("execd: %s: %s", method, resp.Error.Message)
		}
		return json.Unmarshal(resp.Result, out)
	}
}

func (c *Client) readLoop() {
	dec := json.NewDecoder(c.conn)
	for {
		var msg struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			_ = c.Close()
			return
		}
		if msg.ID == nil {
			c.mu.Lock()
			fn := c.handlers[msg.Method]
			c.mu.Unlock()
			if fn != nil {
				fn(msg.Params)
			}
			continue
		}
		c.mu.Lock()
		ch, ok := c.pending[*msg.ID]
		if ok {
			delete(c.pending, *msg.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- Response{JSONRPC: "2.0", ID: msg.ID, Result: msg.Result, Error: msg.Error}
		}
	}
}
