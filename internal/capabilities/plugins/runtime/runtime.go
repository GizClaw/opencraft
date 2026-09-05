// Package runtime hosts subprocess capability plugins: separate
// executables that implement domain logic (e.g. the SSO auth
// protocol) and talk to the host over line-delimited JSON-RPC 2.0 on
// stdin/stdout. The host only understands method names + JSON payloads;
// it never interprets domain semantics, and secrets never appear in
// RPC results (plugins persist them via the secret.* primitives).
package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
)

const (
	// DefaultHandshakeTimeout bounds the plugin's initial handshake.
	DefaultHandshakeTimeout = 5 * time.Second
	// DefaultCallTimeout bounds one host→plugin call.
	DefaultCallTimeout = 30 * time.Second
	// ProtocolVersion is the wire protocol version plugins must report.
	ProtocolVersion = 1
)

// Capability is the manifest-declared runtime of one plugin.
type Capability struct {
	// Binary is the executable path relative to the plugin directory.
	Binary string `json:"binary"`
	// Protocol is the wire protocol version this binary speaks.
	Protocol int `json:"protocol"`
	// Hosts lists hostnames the plugin may open via the open.url
	// primitive (SSRF guard).
	Hosts []string `json:"hosts,omitempty"`
}

// InferenceProfile is a plugin-submitted inference provider profile.
// The host validates and writes it but does not interpret its domain
// meaning (gateway, session, ...). ID is the full stable provider
// instance id and must be unique across the user's inference config;
// it is independent of the plugin id, so one plugin can submit several
// profiles. Ownership is recorded separately by the host.
type InferenceProfile struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Name     string         `json:"name"`
	API      string         `json:"api"`
	Endpoint string         `json:"endpoint"`
	Models   []ProfileModel `json:"models"`
	KeyRef   string         `json:"key_ref"`
	// ProviderSpec carries provider-specific spec options (for example
	// openai's chat_stream_options) as an opaque bag. The host writes
	// them into the provider spec; keys the host manages itself are
	// rejected.
	ProviderSpec map[string]any `json:"provider_spec,omitempty"`
}

// ProfileModel is one model in an inference profile. Capabilities are
// declared as canonical content-kind lists (inputs/outputs).
type ProfileModel struct {
	Name               string            `json:"name"`
	Inputs             []string          `json:"inputs,omitempty"`
	Outputs            []string          `json:"outputs,omitempty"`
	Reasoning          string            `json:"reasoning,omitempty"`
	ReasoningEffortMap map[string]string `json:"reasoning_effort_map,omitempty"`
	EffortNone         bool              `json:"effort_none,omitempty"`
	WebSearch          bool              `json:"web_search,omitempty"`
	Endpoint           string            `json:"endpoint,omitempty"`
}

// InferenceHandler is the host-side write path for inference profiles.
type InferenceHandler struct {
	// Upsert writes (or replaces) one provider profile.
	Upsert func(pluginID string, profile InferenceProfile) error
	// Remove deletes one provider deployment by id.
	Remove func(pluginID, id string) error
}

// SessionImportRequest asks the host to import a session bundle the
// plugin wrote to disk. BundlePath is used instead of inline messages
// because the JSON-RPC transport is line-delimited and capped by
// bufio.Scanner's default 64 KiB token limit.
type SessionImportRequest struct {
	BundlePath string `json:"bundle_path"`
	Title      string `json:"title"`
	Source     string `json:"source"`
	// Workspace optionally selects a previously opened workspace. When
	// empty the host imports into the currently active workspace.
	Workspace string `json:"workspace,omitempty"`
}

// SessionImportResult is returned after history and memory are seeded.
type SessionImportResult struct {
	SessionID string `json:"session_id"`
	Messages  int    `json:"messages"`
	Turns     int    `json:"turns"`
}

// SessionImportStatusRequest asks which of the given import sources
// already exist as import-ready sessions in a workspace's store.
type SessionImportStatusRequest struct {
	// Sources are exact ImportRequest.Source values (for example
	// "codex:<session-id>").
	Sources []string `json:"sources"`
	// Workspace optionally selects a previously opened workspace. When
	// empty the host answers for the currently active workspace.
	Workspace string `json:"workspace,omitempty"`
}

// SessionImportHandler is the host-side write path for session.import.
type SessionImportHandler struct {
	// Import imports bundlePath into the requested workspace and
	// returns the new session id.
	Import func(pluginID string, req SessionImportRequest) (SessionImportResult, error)
	// ImportedSources reports which of the requested sources already
	// exist, mapping each imported source to its session id.
	ImportedSources func(pluginID string, req SessionImportStatusRequest) (map[string]string, error)
}

// WorkspaceHandler answers one capability plugin's question about the
// host's current workspace. The answer is read dynamically on every
// call because capability subprocesses are long-lived and do not
// observe environment-variable changes across workspace switches.
type WorkspaceHandler struct {
	// Current returns the currently active workspace path.
	Current func() (string, error)
}

// SecretStore is the minimal credential surface exposed to plugins as
// the secret.* primitives. Values never cross the JS boundary.
type SecretStore interface {
	Get(ctx context.Context, name string) (value string, found bool, err error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// AllowedSecretScopes is the closed set of namespaces a plugin may
// touch through secret.*. Kept in sync with plugins.AllowedSecretScopes.
var AllowedSecretScopes = map[string]bool{"auth": true, "inference": true}

// Loader resolves a plugin's declared capability and its binary path.
type Loader interface {
	// Capability returns the manifest-declared runtime for id.
	Capability(id string) (Capability, bool, error)
	// BinaryPath resolves and validates the executable path for id.
	BinaryPath(id string, cap Capability) (string, error)
}

// Manager owns the subprocess plugins. Processes are started lazily on
// first Invoke and stopped via Stop / Shutdown.
type Manager struct {
	loader        Loader
	root          string
	secrets       SecretStore
	openURL       func(url string)
	log           io.Writer
	inference     InferenceHandler
	sessionImport SessionImportHandler
	workspace     WorkspaceHandler

	handshakeTimeout time.Duration
	callTimeout      time.Duration
	env              []string

	mu    sync.Mutex
	procs map[string]*process

	baseCtx context.Context
	cancel  context.CancelFunc
}

// NewManager returns a manager rooted at the plugin directory.
func NewManager(root string, loader Loader, secrets SecretStore) *Manager {
	// Manager-owned lifecycle: capability calls and cleanups outlive
	// individual requests, and Shutdown cancels the base context.
	baseCtx, cancel := context.WithCancel(context.Background())
	return &Manager{
		loader:           loader,
		root:             root,
		secrets:          secrets,
		log:              io.Discard,
		baseCtx:          baseCtx,
		cancel:           cancel,
		handshakeTimeout: DefaultHandshakeTimeout,
		callTimeout:      DefaultCallTimeout,
		procs:            make(map[string]*process),
	}
}

// SetOpenURL wires the system-browser opener (nil-safe).
func (m *Manager) SetOpenURL(fn func(url string)) { m.openURL = fn }

// SetLogger attaches a writer for plugin stderr forwarding.
func (m *Manager) SetLogger(w io.Writer) {
	if w != nil {
		m.log = w
	}
}

// SetInferenceHandler wires the inference profile write path.
func (m *Manager) SetInferenceHandler(h InferenceHandler) {
	m.inference = h
}

// SetSessionImportHandler wires the host's session import write path.
func (m *Manager) SetSessionImportHandler(h SessionImportHandler) {
	m.sessionImport = h
}

// SetWorkspaceHandler wires the host's current-workspace query.
func (m *Manager) SetWorkspaceHandler(h WorkspaceHandler) {
	m.workspace = h
}

// SetEnv adds extra environment variables for plugin processes
// (testing helpers, locale overrides, ...).
func (m *Manager) SetEnv(env []string) {
	m.env = append([]string(nil), env...)
}

// Invoke calls method on the capability plugin id with args (JSON
// marshalable) and returns the raw result JSON.
func (m *Manager) Invoke(ctx context.Context, id, method string, args any) (json.RawMessage, error) {
	p, err := m.get(ctx, id)
	if err != nil {
		return nil, err
	}
	return p.call(ctx, method, args)
}

// Stop terminates the plugin process for id, if running.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	p, ok := m.procs[id]
	if ok {
		delete(m.procs, id)
	}
	m.mu.Unlock()
	if ok {
		p.stop()
	}
}

// StopAll terminates every running capability process. Unlike
// Shutdown it keeps the manager usable: the next Invoke restarts a
// process with the manager's current environment.
func (m *Manager) StopAll() {
	m.mu.Lock()
	procs := make([]*process, 0, len(m.procs))
	for id, p := range m.procs {
		delete(m.procs, id)
		procs = append(procs, p)
	}
	m.mu.Unlock()
	for _, p := range procs {
		p.stop()
	}
}

// Cleanup asks a running capability plugin to clean up its own
// resources (inference profile, secrets) via lifecycle.cleanup, then
// stops it. Best-effort: a plugin without a running process or without
// cleanup support leaves the host fallback to remove leftovers.
func (m *Manager) Cleanup(id string) error {
	m.mu.Lock()
	p, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.baseCtx, 10*time.Second)
	defer cancel()
	_, err := p.call(ctx, "lifecycle.cleanup", map[string]any{})
	m.Stop(id)
	return err
}

// Shutdown stops every running plugin process.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	procs := make([]*process, 0, len(m.procs))
	for id, p := range m.procs {
		procs = append(procs, p)
		delete(m.procs, id)
	}
	m.mu.Unlock()
	for _, p := range procs {
		p.stop()
	}
	if m.cancel != nil {
		m.cancel()
	}
}

// get returns the running process for id, starting and handshaking it
// on first use.
func (m *Manager) get(ctx context.Context, id string) (*process, error) {
	m.mu.Lock()
	if p, ok := m.procs[id]; ok {
		m.mu.Unlock()
		return p, nil
	}
	cap, ok, err := m.loader.Capability(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("runtime: plugin %q declares no capability binary", id)
	}
	bin, err := m.loader.BinaryPath(id, cap)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if cap.Protocol != ProtocolVersion {
		m.mu.Unlock()
		return nil, fmt.Errorf(
			"runtime: plugin %q protocol %d != host %d", id, cap.Protocol, ProtocolVersion)
	}
	p, err := m.start(id, cap, bin)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.procs[id] = p
	m.mu.Unlock()

	// Handshake: the plugin must announce itself within the timeout.
	select {
	case <-p.ready:
		return p, nil
	case <-p.done:
		return nil, fmt.Errorf("runtime: plugin %q exited during handshake", id)
	case <-time.After(m.handshakeTimeout):
		p.stop()
		return nil, fmt.Errorf("runtime: plugin %q handshake timeout", id)
	case <-ctx.Done():
		p.stop()
		return nil, ctx.Err()
	}
}

func (m *Manager) start(id string, cap Capability, bin string) (*process, error) {
	cmd := exec.Command(bin)
	if len(m.env) > 0 {
		cmd.Env = append(os.Environ(), m.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("runtime: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("runtime: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("runtime: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runtime: start %q: %w", bin, err)
	}
	p := &process{
		manager: m,
		id:      id,
		cap:     cap,
		cmd:     cmd,
		stdin:   stdin,
		nextID:  1,
		pending: make(map[int]chan rpcResponse),
		done:    make(chan struct{}),
		ready:   make(chan struct{}),
	}
	go p.readLoop(stdout)
	go func() {
		_, copyErr := io.Copy(m.log, stderr)
		telemetry.WarnErr(m.baseCtx,
			"plugin runtime: drain capability stderr failed", copyErr)
	}()
	go func() {
		<-p.done
		m.mu.Lock()
		if m.procs[id] == p {
			delete(m.procs, id)
		}
		m.mu.Unlock()
	}()
	return p, nil
}

// process is one running capability plugin.
type process struct {
	manager *Manager
	id      string
	cap     Capability
	cmd     *exec.Cmd
	stdin   io.WriteCloser

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse
	ready   chan struct{}
	done    chan struct{}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends one host→plugin request and waits for its response.
func (p *process) call(ctx context.Context, method string, args any) (json.RawMessage, error) {
	select {
	case <-p.ready:
	case <-p.done:
		return nil, errors.New("runtime: plugin process exited")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	params, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("runtime: marshal args: %w", err)
	}
	p.mu.Lock()
	id := p.nextID
	p.nextID++
	ch := make(chan rpcResponse, 1)
	p.pending[id] = ch
	p.mu.Unlock()

	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(strconv.Itoa(id)),
		Method:  method,
		Params:  params,
	}
	if err := p.write(req); err != nil {
		p.drop(id)
		return nil, err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, &RPCCallError{Method: method, Message: resp.Error.Message}
		}
		return resp.Result, nil
	case <-time.After(p.manager.callTimeout):
		p.drop(id)
		return nil, fmt.Errorf("runtime: plugin %q call %s timeout", p.id, method)
	case <-p.done:
		return nil, errors.New("runtime: plugin process exited")
	case <-ctx.Done():
		p.drop(id)
		return nil, ctx.Err()
	}
}

// RPCCallError is a non-zero JSON-RPC error returned by the plugin.
type RPCCallError struct {
	Method  string
	Message string
}

func (e *RPCCallError) Error() string {
	return fmt.Sprintf("runtime: plugin %s: %s", e.Method, e.Message)
}

func (p *process) drop(id int) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

func (p *process) write(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(raw, '\n'))
	return err
}

func (p *process) readLoop(r io.Reader) {
	defer close(p.done)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Bytes()
		var probe struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		if probe.Method != "" {
			p.handleRequest(line)
		} else {
			p.handleResponse(line)
		}
	}
}

func (p *process) handleResponse(line []byte) {
	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return
	}
	id, err := strconv.Atoi(strings.Trim(string(resp.ID), `"`))
	if err != nil {
		return
	}
	p.mu.Lock()
	ch, ok := p.pending[id]
	if ok {
		delete(p.pending, id)
	}
	p.mu.Unlock()
	if ok {
		ch <- resp
	}
}

func (p *process) handleRequest(line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: decode capability request failed", err)
		return
	}
	// The handshake is a plugin→host request with no response writer
	// yet; it is handled specially.
	if req.Method == "handshake" {
		p.handleHandshake(req)
		return
	}
	select {
	case <-p.ready:
	default:
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: send handshake-required response failed",
			p.respondError(req, -32001, "handshake required"))
		return
	}
	result, err := p.manager.handlePrimitive(p, req)
	if err != nil {
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: send primitive error response failed",
			p.respondError(req, -32000, err.Error()))
		return
	}
	telemetry.WarnErr(p.manager.baseCtx,
		"plugin runtime: send primitive response failed",
		p.respond(req, result))
}

func (p *process) handleHandshake(req rpcRequest) {
	var hs struct {
		ID       string `json:"id"`
		Protocol int    `json:"protocol"`
	}
	if err := json.Unmarshal(req.Params, &hs); err != nil {
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: decode capability handshake failed", err)
	}
	select {
	case <-p.ready:
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: send duplicate handshake response failed",
			p.respondError(req, -32000, "duplicate handshake"))
		return
	default:
	}
	if hs.ID != p.id || hs.Protocol != ProtocolVersion {
		telemetry.WarnErr(p.manager.baseCtx,
			"plugin runtime: send handshake mismatch response failed",
			p.respondError(req, -32002, "handshake mismatch"))
		p.stop()
		return
	}
	close(p.ready)
	telemetry.WarnErr(p.manager.baseCtx,
		"plugin runtime: send handshake response failed",
		p.respond(req, map[string]any{"ok": true}))
}

func (p *process) respond(req rpcRequest, result any) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return p.write(rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  raw,
	})
}

func (p *process) respondError(req rpcRequest, code int, message string) error {
	return p.write(rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &rpcError{Code: code, Message: message},
	})
}

func (p *process) stop() {
	select {
	case <-p.done:
		return
	default:
	}
	telemetry.WarnErr(p.manager.baseCtx,
		"plugin runtime: kill capability process failed", p.cmd.Process.Kill())
	telemetry.WarnErr(p.manager.baseCtx,
		"plugin runtime: close capability stdin failed", p.stdin.Close())
}

// handlePrimitive executes one plugin→host primitive request.
func (m *Manager) handlePrimitive(p *process, req rpcRequest) (any, error) {
	switch req.Method {
	case "secret.get", "secret.set", "secret.delete":
		return m.handleSecret(p, req)
	case "open.url":
		return m.handleOpenURL(p, req)
	case "inference.upsert":
		return m.handleInferenceUpsert(p, req)
	case "inference.remove":
		return m.handleInferenceRemove(p, req)
	case "session.import":
		return m.handleSessionImport(p, req)
	case "session.imported_sources":
		return m.handleSessionImportedSources(p, req)
	case "workspace.current":
		return m.handleWorkspaceCurrent()
	case "emit.event":
		// Reserved: forward to the host event bus once wired.
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("runtime: unknown primitive %q", req.Method)
	}
}

func (m *Manager) handleWorkspaceCurrent() (any, error) {
	if m.workspace.Current == nil {
		return nil, errors.New("runtime: workspace handler unavailable")
	}
	path, err := m.workspace.Current()
	if err != nil {
		return nil, fmt.Errorf("runtime: workspace.current: %w", err)
	}
	return map[string]any{"workspace": path}, nil
}

func (m *Manager) handleSessionImport(p *process, req rpcRequest) (any, error) {
	var args SessionImportRequest
	if err := json.Unmarshal(req.Params, &args); err != nil {
		return nil, fmt.Errorf("runtime: session.import args: %w", err)
	}
	if strings.TrimSpace(args.BundlePath) == "" {
		return nil, errors.New("runtime: session.import bundle_path is required")
	}
	if m.sessionImport.Import == nil {
		return nil, errors.New("runtime: session import handler unavailable")
	}
	return m.sessionImport.Import(p.id, args)
}

func (m *Manager) handleSessionImportedSources(p *process, req rpcRequest) (any, error) {
	var args SessionImportStatusRequest
	if err := json.Unmarshal(req.Params, &args); err != nil {
		return nil, fmt.Errorf("runtime: session.imported_sources args: %w", err)
	}
	if m.sessionImport.ImportedSources == nil {
		return nil, errors.New("runtime: session imported-sources handler unavailable")
	}
	return m.sessionImport.ImportedSources(p.id, args)
}

func (m *Manager) handleInferenceUpsert(p *process, req rpcRequest) (any, error) {
	var profile InferenceProfile
	if err := json.Unmarshal(req.Params, &profile); err != nil {
		return nil, fmt.Errorf("runtime: inference.upsert args: %w", err)
	}
	if m.inference.Upsert == nil {
		return nil, errors.New("runtime: inference upsert handler unavailable")
	}
	return map[string]any{}, m.inference.Upsert(p.id, profile)
}

func (m *Manager) handleInferenceRemove(p *process, req rpcRequest) (any, error) {
	var args struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &args); err != nil {
		return nil, fmt.Errorf("runtime: inference.remove args: %w", err)
	}
	if m.inference.Remove == nil {
		return nil, errors.New("runtime: inference remove handler unavailable")
	}
	return map[string]any{}, m.inference.Remove(p.id, args.ID)
}

func (m *Manager) handleSecret(p *process, req rpcRequest) (any, error) {
	var args struct {
		Scope string `json:"scope"`
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(req.Params, &args); err != nil {
		return nil, fmt.Errorf("runtime: secret args: %w", err)
	}
	if !AllowedSecretScopes[args.Scope] {
		return nil, fmt.Errorf("runtime: unknown secret scope %q", args.Scope)
	}
	// The plugin may only touch its own namespace: scope/<id>/...
	if !strings.HasPrefix(args.Name, p.id+"/") {
		return nil, fmt.Errorf("runtime: secret %q outside plugin namespace", args.Name)
	}
	account := args.Scope + "/" + args.Name
	ctx := m.baseCtx
	switch req.Method {
	case "secret.get":
		if m.secrets == nil {
			return nil, errors.New("runtime: secret store unavailable")
		}
		v, found, err := m.secrets.Get(ctx, account)
		if err != nil {
			return nil, err
		}
		return map[string]any{"found": found, "value": v}, nil
	case "secret.set":
		if m.secrets == nil {
			return nil, errors.New("runtime: secret store unavailable")
		}
		return map[string]any{}, m.secrets.Set(ctx, account, args.Value)
	case "secret.delete":
		if m.secrets == nil {
			return nil, errors.New("runtime: secret store unavailable")
		}
		return map[string]any{}, m.secrets.Delete(ctx, account)
	}
	return nil, errors.New("runtime: unreachable")
}

func (m *Manager) handleOpenURL(p *process, req rpcRequest) (any, error) {
	var args struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(req.Params, &args); err != nil {
		return nil, fmt.Errorf("runtime: open.url args: %w", err)
	}
	u, err := url.Parse(args.URL)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("runtime: invalid url %q", args.URL)
	}
	if !allowedHost(p.cap.Hosts, u.Hostname()) {
		return nil, fmt.Errorf("runtime: host %q not allowed", u.Hostname())
	}
	if m.openURL != nil {
		m.openURL(args.URL)
	}
	return map[string]any{}, nil
}

func allowedHost(allowed []string, host string) bool {
	for _, h := range allowed {
		if strings.EqualFold(h, host) {
			return true
		}
	}
	return false
}

// DefaultLoader resolves capabilities from the plugin Store.
type DefaultLoader struct {
	// CapabilityFunc returns the declared capability for id.
	CapabilityFunc func(id string) (Capability, bool, error)
	// DirFunc resolves where an installed plugin lives and whether it
	// is a user copy. When set, BinaryPath uses it to refuse falling
	// back to a builtin binary for a user plugin that shadows a
	// builtin (the manifests could disagree).
	DirFunc func(id string) (dir string, builtin bool, err error)
	// Root is the plugin directory.
	Root string
}

func (l DefaultLoader) Capability(id string) (Capability, bool, error) {
	if l.CapabilityFunc == nil {
		return Capability{}, false, nil
	}
	return l.CapabilityFunc(id)
}

func (l DefaultLoader) BinaryPath(id string, cap Capability) (string, error) {
	bin := filepath.Clean(cap.Binary)
	if bin == "" || filepath.IsAbs(bin) ||
		bin == ".." || strings.HasPrefix(bin, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("runtime: capability binary escapes plugin dir: %q", cap.Binary)
	}
	path := filepath.Join(l.Root, id, bin)
	if _, err := os.Stat(path); err != nil {
		// Not in the user plugin dir. A user copy that shadows a
		// builtin must not silently use the builtin's binary; only
		// plugins that live in the builtin root may fall back.
		if l.DirFunc != nil {
			_, builtin, err := l.DirFunc(id)
			if err != nil {
				return "", fmt.Errorf("runtime: resolve plugin %q: %w", id, err)
			}
			if !builtin {
				return "", fmt.Errorf(
					"runtime: capability binary %q missing from user plugin %q",
					bin, id)
			}
		}
		// Fall back to the read-only builtin (app-bundled) plugin
		// directory.
		if root := BuiltinPluginRoot(); root != "" {
			path = filepath.Join(root, id, bin)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("runtime: capability binary %q: %w", bin, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("runtime: capability binary %q is a directory", bin)
	}
	return path, nil
}

// BuiltinPluginRoot returns the read-only, app-bundled plugin
// directory next to the running executable, or "" when absent (dev
// runs, platforms without a bundled layout). The bundle layout is
// platform-specific: macOS apps ship plugins under
// Contents/Resources/plugins; other platforms keep a plugins/
// directory next to the binary.
func BuiltinPluginRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	var root string
	if goruntime.GOOS == "darwin" {
		root = filepath.Join(exeDir, "..", "Resources", "plugins")
	} else {
		root = filepath.Join(exeDir, "plugins")
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}
