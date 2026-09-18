package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GizClaw/opencraft/internal/testing/logcapture"
)

// helperPlugin simulates a capability plugin: it handshakes, then
// answers auth.* calls and exercises the secret.set primitive.
func helperPlugin() {
	sc := bufio.NewScanner(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	write := func(s string) {
		_, _ = out.WriteString(s + "\n")
		_ = out.Flush()
	}
	write(`{"jsonrpc":"2.0","id":1,"method":"handshake","params":{"id":"test-plugin","protocol":1}}`)
	for sc.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		switch req.Method {
		case "auth.begin":
			// Exercise a plugin→host primitive before answering.
			write(`{"jsonrpc":"2.0","id":99,"method":"secret.set","params":{"scope":"auth","name":"test-plugin/token","value":"aig_test"}}`)
			sc.Scan() // consume the primitive response
			write(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":{"user_code":"ABCD-EFGH","interval_sec":3}}`,
				req.ID))
		case "auth.poll":
			write(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":{"status":"ok"}}`, req.ID))
		case "auth.big":
			// A response far above bufio.Scanner's 64 KiB default: the
			// host has to accept it instead of ending the session.
			write(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":{"blob":%q}}`,
				req.ID, strings.Repeat("x", bigResponseBytes)))
		case "auth.fail":
			write(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"boom"}}`, req.ID))
		case "lifecycle.cleanup":
			fmt.Fprintln(os.Stderr, "CLEANUP_CALLED")
			write(fmt.Sprintf(
				`{"jsonrpc":"2.0","id":%s,"result":{}}`, req.ID))
		}
	}
}

// malformedPlugin emits a non-JSON line and exits, exercising the
// handshake-failure path for hostile/broken plugins.
func malformedPlugin() {
	_, _ = fmt.Fprintln(os.Stdout, "this is not json")
	os.Exit(0)
}

// oversizedPlugin emits a line beyond the scanner's cap and exits,
// exercising the read-loop teardown path.
func oversizedPlugin() {
	_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", stdoutLineLimit+1024))
	os.Exit(0)
}

// oversizedOutputPlugin handshakes, then emits a line beyond the
// scanner's cap and keeps running: the host has to notice the failure and
// stop it rather than leave it blocked on a full pipe.
func oversizedOutputPlugin() {
	_, _ = fmt.Fprintln(os.Stdout,
		`{"jsonrpc":"2.0","id":1,"method":"handshake","params":{"id":"test-plugin","protocol":1}}`)
	_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", stdoutLineLimit+1024))
	time.Sleep(30 * time.Second)
}

// crashingPlugin handshakes and then exits on its own, simulating a
// capability process that died after announcing itself. It explains
// itself on stderr, which is the only channel a plugin has for that.
func crashingPlugin() {
	_, _ = fmt.Fprintln(os.Stdout,
		`{"jsonrpc":"2.0","id":1,"method":"handshake","params":{"id":"test-plugin","protocol":1}}`)
	_, _ = fmt.Fprintln(os.Stderr, "helper plugin: crashing on purpose")
	time.Sleep(50 * time.Millisecond)
	os.Exit(1)
}

func TestHelperProcess(t *testing.T) {
	switch os.Getenv("GO_WANT_HELPER_PROCESS") {
	case "1":
		helperPlugin()
		os.Exit(0)
	case "2":
		malformedPlugin()
	case "3":
		oversizedPlugin()
	case "4":
		crashingPlugin()
	case "5":
		oversizedOutputPlugin()
	}
}

// bigResponseBytes is the payload of the auth.big helper response: well
// past the scanner's 64 KiB default and far below the host's cap.
const bigResponseBytes = 1 << 20

type memSecrets struct {
	m map[string]string
}

func (s *memSecrets) Get(_ context.Context, name string) (string, bool, error) {
	v, ok := s.m[name]
	return v, ok, nil
}

func (s *memSecrets) Set(_ context.Context, name, value string) error {
	s.m[name] = value
	return nil
}

func (s *memSecrets) Delete(_ context.Context, name string) error {
	delete(s.m, name)
	return nil
}

type testLoader struct {
	cap Capability
	bin string
}

func (l testLoader) Capability(string) (Capability, bool, error) {
	return l.cap, true, nil
}

func (l testLoader) BinaryPath(string, Capability) (string, error) {
	return l.bin, nil
}

func newTestManager(t *testing.T) (*Manager, *memSecrets) {
	t.Helper()
	sec := &memSecrets{m: map[string]string{}}
	loader := testLoader{
		cap: Capability{Binary: "helper", Protocol: 1},
		bin: os.Args[0],
	}
	m := NewManager(t.TempDir(), loader, sec)
	m.SetEnv([]string{"GO_WANT_HELPER_PROCESS=1"})
	m.SetTimeouts(0, 2*time.Second)
	return m, sec
}

func newTestManagerWithHelper(t *testing.T, mode string) (*Manager, *memSecrets) {
	t.Helper()
	sec := &memSecrets{m: map[string]string{}}
	loader := testLoader{
		cap: Capability{Binary: "helper", Protocol: 1},
		bin: os.Args[0],
	}
	m := NewManager(t.TempDir(), loader, sec)
	m.SetEnv([]string{"GO_WANT_HELPER_PROCESS=" + mode})
	m.SetTimeouts(2*time.Second, 2*time.Second)
	return m, sec
}

// TestRedactStderrLine pins the stderr redaction contract: a plugin that
// prints a connection URL must not put the credential values into the
// host log, while the rest of the line (including the parameter names)
// stays readable.
func TestRedactStderrLine(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			in:   "sdk: connected to wss://host/ws/v2?fpid=493&access_key=SECRET&ticket=TICKET[conn_id=1]",
			want: "sdk: connected to wss://host/ws/v2?fpid=493&access_key=***&ticket=***[conn_id=1]",
		},
		{
			in:   "retry https://api.example/v1?token=abc&page=2",
			want: "retry https://api.example/v1?token=***&page=2",
		},
		{in: "plain diagnostic, nothing to hide", want: "plain diagnostic, nothing to hide"},
		{in: "flags: --key=value", want: "flags: --key=value"},
	}
	for _, tc := range cases {
		if got := redactStderrLine(tc.in); got != tc.want {
			t.Errorf("redactStderrLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestMalformedPluginFailsHandshake(t *testing.T) {
	m, _ := newTestManagerWithHelper(t, "2")
	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.begin", nil); err == nil {
		t.Fatal("malformed plugin unexpectedly answered")
	}
}

func TestOversizedPluginFailsHandshake(t *testing.T) {
	m, _ := newTestManagerWithHelper(t, "3")
	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.begin", nil); err == nil {
		t.Fatal("oversized plugin unexpectedly answered")
	}
}

// TestLargeResponseIsAccepted covers the root cause behind the
// oversized-output path: a response bigger than the scanner's 64 KiB
// default used to end the read loop, which left the plugin blocked on a
// full pipe and made every later call report "process exited".
func TestLargeResponseIsAccepted(t *testing.T) {
	m, _ := newTestManager(t)
	res, err := m.Invoke(context.Background(), "test-plugin", "auth.big", nil)
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	var out struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Blob) != bigResponseBytes {
		t.Fatalf("blob = %d bytes, want %d", len(out.Blob), bigResponseBytes)
	}
}

// TestOversizedOutputStopsPlugin pins what happens when a plugin's output
// does exceed the cap: the host reports the read failure with the plugin
// id and stops the process, instead of leaving it running and blocked on
// a pipe nobody drains.
func TestOversizedOutputStopsPlugin(t *testing.T) {
	capture := logcapture.Install(t)
	m, _ := newTestManagerWithHelper(t, "5")
	p, err := m.get(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("plugin with oversized output was not stopped")
	}
	if !p.hostStopped.Load() {
		t.Fatal("the host did not stop the plugin it could no longer read")
	}
	var seen bool
	for _, record := range capture.Records() {
		if record.Body().AsString() !=
			"plugin runtime: read capability output failed" {
			continue
		}
		if got := logcapture.Attribute(record, "plugin.id"); got != "test-plugin" {
			t.Fatalf("plugin.id = %q, want test-plugin", got)
		}
		seen = true
	}
	if !seen {
		t.Fatalf("read failure was not logged: %v", capture.Bodies())
	}
}

// TestGetReplacesFinishedProcess pins the isolation rule: a cached
// process whose done channel is already closed must not be handed out,
// or one dead plugin would fail every caller until its exit watcher
// finally removed it.
func TestGetReplacesFinishedProcess(t *testing.T) {
	m, _ := newTestManager(t)
	dead := &process{
		manager: m,
		id:      "test-plugin",
		done:    make(chan struct{}),
	}
	close(dead.done)
	m.mu.Lock()
	m.procs["test-plugin"] = dead
	m.mu.Unlock()

	got, err := m.get(context.Background(), "test-plugin")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer got.stop()
	if got == dead {
		t.Fatal("get returned a finished process")
	}
}

func TestInvokeAndPrimitive(t *testing.T) {
	m, sec := newTestManager(t)
	ctx := context.Background()

	var opened []string
	m.SetOpenURL(func(u string) { opened = append(opened, u) })

	res, err := m.Invoke(ctx, "test-plugin", "auth.begin", map[string]any{})
	if err != nil {
		t.Fatalf("auth.begin: %v", err)
	}
	var begin struct {
		UserCode    string `json:"user_code"`
		IntervalSec int    `json:"interval_sec"`
	}
	if err := json.Unmarshal(res, &begin); err != nil {
		t.Fatalf("decode begin result: %v", err)
	}
	if begin.UserCode != "ABCD-EFGH" || begin.IntervalSec != 3 {
		t.Fatalf("unexpected begin result: %+v", begin)
	}
	// The plugin must have persisted its token via the secret.set
	// primitive during auth.begin.
	if sec.m["auth/test-plugin/token"] != "aig_test" {
		t.Fatalf("token not persisted via primitive: %v", sec.m)
	}

	res, err = m.Invoke(ctx, "test-plugin", "auth.poll", map[string]any{})
	if err != nil {
		t.Fatalf("auth.poll: %v", err)
	}
	if !strings.Contains(string(res), `"ok"`) {
		t.Fatalf("unexpected poll result: %s", res)
	}
}

func TestInvokeError(t *testing.T) {
	m, _ := newTestManager(t)
	_, err := m.Invoke(context.Background(), "test-plugin", "auth.fail", nil)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got: %v", err)
	}
}

func TestHandshakeMismatch(t *testing.T) {
	m, _ := newTestManager(t)
	// The helper reports protocol 1 with id test-plugin, so spoof the
	// manager's expectation to force a mismatch.
	m.mu.Lock()
	for id := range m.procs {
		delete(m.procs, id)
	}
	m.mu.Unlock()
	_, err := m.Invoke(context.Background(), "other-plugin", "auth.begin", nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin")
	}
}

func TestSecretScopeGuard(t *testing.T) {
	m, sec := newTestManager(t)
	// A primitive touching another plugin's namespace must be refused.
	_, err := m.handleSecret(&process{id: "test-plugin"}, rpcRequest{
		Method: "secret.set",
		Params: json.RawMessage(`{"scope":"auth","name":"other-plugin/token","value":"x"}`),
	})
	if err == nil {
		t.Fatal("expected namespace guard error")
	}
	if len(sec.m) != 0 {
		t.Fatalf("secrets mutated: %v", sec.m)
	}
}

func TestInferencePrimitivesForwardPluginAndInstanceIDs(t *testing.T) {
	m, _ := newTestManager(t)
	var upsertedPlugin, upsertedID string
	var upsertedScope string
	var removedPlugin, removedID string
	m.SetInferenceHandler(InferenceHandler{
		Upsert: func(pluginID string, profile InferenceProfile) error {
			upsertedPlugin = pluginID
			upsertedID = profile.StableID
			upsertedScope = profile.Advanced.ReasoningScope
			return nil
		},
		Remove: func(pluginID, id string) error {
			removedPlugin = pluginID
			removedID = id
			return nil
		},
	})

	if _, err := m.handleInferenceUpsert(&process{id: "plug"}, rpcRequest{
		Params: json.RawMessage(`{
			"stable_id": "plug-gateway",
			"type": "openai",
			"key_ref": "auth/plug/token",
			"advanced": {
				"reasoning_scope": "gateway-2026"
			}
		}`),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upsertedPlugin != "plug" || upsertedID != "plug-gateway" {
		t.Fatalf("upsert forwarded %q/%q", upsertedPlugin, upsertedID)
	}
	if upsertedScope != "gateway-2026" {
		t.Fatalf("upsert reasoning scope = %q", upsertedScope)
	}
	if _, err := m.handleInferenceRemove(&process{id: "plug"}, rpcRequest{
		Params: json.RawMessage(`{"id":"plug-embed"}`),
	}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removedPlugin != "plug" || removedID != "plug-embed" {
		t.Fatalf("remove forwarded %q/%q", removedPlugin, removedID)
	}
}

func TestTelemetryPrimitives(t *testing.T) {
	m, _ := newTestManager(t)
	var calls []string
	m.SetTelemetryHandler(TelemetryHandler{
		Configure: func(pluginID string, req TelemetryExportRequest) error {
			calls = append(calls, pluginID+":"+req.Endpoint+":"+
				req.Headers["Authorization"])
			return nil
		},
		Disable: func(pluginID string) error {
			calls = append(calls, "disable:"+pluginID)
			return nil
		},
	})

	res, err := m.handleTelemetryConfigure(&process{id: "exporter"}, rpcRequest{
		Method: "telemetry.configure",
		Params: json.RawMessage(
			`{"endpoint":"collector.example:4318","headers":{"Authorization":"Bearer t"}}`),
	})
	if err != nil {
		t.Fatalf("handleTelemetryConfigure: %v", err)
	}
	if len(calls) != 1 ||
		calls[0] != "exporter:collector.example:4318:Bearer t" {
		t.Fatalf("configure calls = %q", calls)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"configured":true`) {
		t.Fatalf("unexpected result: %s", data)
	}

	// Unknown fields are rejected so a mistyped plugin argument fails
	// loudly instead of silently configuring a different sink.
	if _, err := m.handleTelemetryConfigure(&process{id: "exporter"}, rpcRequest{
		Method: "telemetry.configure",
		Params: json.RawMessage(
			`{"endpoint":"collector.example:4318","insecure":true,"typo":1}`),
	}); err == nil || !strings.Contains(err.Error(), "telemetry.configure args") {
		t.Fatalf("strict decode error = %v", err)
	}

	if _, err := m.handleTelemetryDisable(&process{id: "exporter"}, rpcRequest{
		Method: "telemetry.disable",
	}); err != nil {
		t.Fatalf("handleTelemetryDisable: %v", err)
	}
	if len(calls) != 2 || calls[1] != "disable:exporter" {
		t.Fatalf("calls after disable = %q", calls)
	}
}

func TestTelemetryPrimitiveWithoutHandler(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.handleTelemetryConfigure(&process{id: "exporter"}, rpcRequest{
		Method: "telemetry.configure",
		Params: json.RawMessage(`{"endpoint":"collector.example:4318"}`),
	}); err == nil {
		t.Fatal("expected an error when no telemetry handler is wired")
	}
	if _, err := m.handleTelemetryDisable(&process{id: "exporter"}, rpcRequest{
		Method: "telemetry.disable",
	}); err == nil {
		t.Fatal("expected an error when no telemetry handler is wired")
	}
}

func TestProcessExitHandlerFiresOnCrash(t *testing.T) {
	m, _ := newTestManagerWithHelper(t, "4")
	exits := make(chan string, 1)
	m.SetProcessExitHandler(func(pluginID string) { exits <- pluginID })

	// The helper handshakes and then dies on its own; the invoke result
	// is irrelevant, only the exit notification matters.
	_, _ = m.Invoke(context.Background(), "test-plugin", "auth.begin", nil)

	select {
	case id := <-exits:
		if id != "test-plugin" {
			t.Fatalf("exit handler got %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("process exit handler did not fire for a crashed plugin")
	}
}

func TestProcessExitHandlerSkipsHostStop(t *testing.T) {
	m, _ := newTestManager(t)
	exits := make(chan string, 1)
	m.SetProcessExitHandler(func(pluginID string) { exits <- pluginID })

	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.poll", nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m.Stop("test-plugin")

	select {
	case id := <-exits:
		t.Fatalf("host stop reported as an unexpected exit: %q", id)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestSessionImportPrimitive(t *testing.T) {
	m, _ := newTestManager(t)
	var gotPlugin string
	var gotReq SessionImportRequest
	m.SetSessionImportHandler(SessionImportHandler{
		Import: func(pluginID string, req SessionImportRequest) (SessionImportResult, error) {
			gotPlugin = pluginID
			gotReq = req
			return SessionImportResult{
				SessionID: "s-imported",
				Messages:  3,
				Turns:     1,
			}, nil
		},
	})

	res, err := m.handleSessionImport(&process{id: "importer"}, rpcRequest{
		Method: "session.import",
		Params: json.RawMessage(
			`{"bundle_path":"/tmp/conv.json","title":"Imported","source":"codex:1"}`),
	})
	if err != nil {
		t.Fatalf("handleSessionImport: %v", err)
	}
	if gotPlugin != "importer" || gotReq.BundlePath != "/tmp/conv.json" ||
		gotReq.Source != "codex:1" {
		t.Fatalf("handler got %q %+v", gotPlugin, gotReq)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"session_id":"s-imported"`) {
		t.Fatalf("unexpected result: %s", data)
	}
}

func TestSessionImportedSourcesPrimitive(t *testing.T) {
	m, _ := newTestManager(t)
	var gotPlugin string
	var gotReq SessionImportStatusRequest
	m.SetSessionImportHandler(SessionImportHandler{
		ImportedSources: func(
			pluginID string,
			req SessionImportStatusRequest,
		) (map[string]string, error) {
			gotPlugin = pluginID
			gotReq = req
			return map[string]string{
				"codex:conv-1": "s-imported-1",
			}, nil
		},
	})

	res, err := m.handleSessionImportedSources(&process{id: "importer"}, rpcRequest{
		Method: "session.imported_sources",
		Params: json.RawMessage(
			`{"sources":["codex:conv-1","codex:conv-2"]}`),
	})
	if err != nil {
		t.Fatalf("handleSessionImportedSources: %v", err)
	}
	if gotPlugin != "importer" ||
		len(gotReq.Sources) != 2 ||
		gotReq.Sources[0] != "codex:conv-1" {
		t.Fatalf("handler got %q %+v", gotPlugin, gotReq)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"codex:conv-1":"s-imported-1"`) {
		t.Fatalf("unexpected result: %s", data)
	}
}

func TestWorkspaceCurrentPrimitive(t *testing.T) {
	m, _ := newTestManager(t)
	m.SetWorkspaceHandler(WorkspaceHandler{
		Current: func() (string, error) {
			return "/live/workspace", nil
		},
	})
	res, err := m.handleWorkspaceCurrent()
	if err != nil {
		t.Fatalf("handleWorkspaceCurrent: %v", err)
	}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"workspace":"/live/workspace"`) {
		t.Fatalf("unexpected result: %s", data)
	}
}

func TestStopShutsDownProcess(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.poll", nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	m.Stop("test-plugin")
	time.Sleep(50 * time.Millisecond)
	m.mu.Lock()
	_, running := m.procs["test-plugin"]
	m.mu.Unlock()
	if running {
		t.Fatal("process still registered after Stop")
	}
}

func TestCleanupNotifiesPlugin(t *testing.T) {
	capture := logcapture.Install(t)
	m, _ := newTestManager(t)
	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.poll", nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if err := m.Cleanup("test-plugin"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if !waitForPluginStderr(capture, "CLEANUP_CALLED") {
		t.Fatalf("plugin cleanup callback not invoked; log=%v",
			capture.Bodies())
	}
	m.mu.Lock()
	_, running := m.procs["test-plugin"]
	m.mu.Unlock()
	if running {
		t.Fatal("process still registered after Cleanup")
	}
}

// TestPluginStderrReachesHostLog pins the contract a plugin relies on
// when it explains itself: whatever it writes to stderr lands in the
// host log, tagged with the plugin id, instead of being discarded.
func TestPluginStderrReachesHostLog(t *testing.T) {
	capture := logcapture.Install(t)
	m, _ := newTestManagerWithHelper(t, "4")

	// The helper handshakes, then dies; the invoke itself may fail,
	// which is not what this test is about.
	_, _ = m.Invoke(context.Background(), "test-plugin", "auth.poll", nil)

	if !waitForPluginStderr(capture, "crashing on purpose") {
		t.Fatalf("plugin stderr missing from the host log: %v",
			capture.Bodies())
	}
	for _, record := range capture.Records() {
		if record.Body().AsString() != pluginStderrBody {
			continue
		}
		if id := logcapture.Attribute(record, "plugin.id"); id != "test-plugin" {
			t.Fatalf("plugin.id = %q, want test-plugin", id)
		}
	}
}

// pluginStderrBody is the message the runtime logs forwarded plugin
// stderr lines under.
const pluginStderrBody = "plugin stderr"

// waitForPluginStderr reports whether one forwarded line contains want.
// The forwarder runs in its own goroutine, so the caller polls.
func waitForPluginStderr(capture *logcapture.Recorder, want string) bool {
	deadline := time.Now().Add(2 * time.Second)
	for {
		for _, record := range capture.Records() {
			if record.Body().AsString() != pluginStderrBody {
				continue
			}
			if strings.Contains(
				logcapture.Attribute(record, "plugin.line"), want,
			) {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// stubWriter is a write-only sink capturing what the host sends to a
// (simulated) capability process.
type stubWriter struct {
	buf bytes.Buffer
}

func (w *stubWriter) Write(p []byte) (int, error) { return w.buf.Write(p) }

func (w *stubWriter) Close() error { return nil }

func TestHandshakeReplyCarriesHostVersion(t *testing.T) {
	m, _ := newTestManager(t)
	m.SetHostVersion("1.2.3-rc.1")

	out := &stubWriter{}
	p := &process{
		manager: m,
		id:      "test-plugin",
		stdin:   out,
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}
	p.handleHandshake(rpcRequest{
		ID:     json.RawMessage(`1`),
		Params: json.RawMessage(`{"id":"test-plugin","protocol":1}`),
	})

	var line []byte
	if sc := bufio.NewScanner(&out.buf); sc.Scan() {
		line = sc.Bytes()
	} else {
		t.Fatal("host did not answer the handshake")
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode handshake reply: %v (%s)", err, line)
	}
	var hs handshakeResult
	if err := json.Unmarshal(resp.Result, &hs); err != nil {
		t.Fatalf("decode handshake result: %v (%s)", err, resp.Result)
	}
	if !hs.Ok || hs.HostVersion != "1.2.3-rc.1" {
		t.Fatalf("unexpected handshake result: %+v", hs)
	}
}

func TestHandshakeReplyReportsUnknownHostVersion(t *testing.T) {
	m, _ := newTestManager(t) // host version is never recorded

	out := &stubWriter{}
	p := &process{
		manager: m,
		id:      "test-plugin",
		stdin:   out,
		ready:   make(chan struct{}),
		done:    make(chan struct{}),
	}
	p.handleHandshake(rpcRequest{
		ID:     json.RawMessage(`1`),
		Params: json.RawMessage(`{"id":"test-plugin","protocol":1}`),
	})

	var line []byte
	if sc := bufio.NewScanner(&out.buf); sc.Scan() {
		line = sc.Bytes()
	} else {
		t.Fatal("host did not answer the handshake")
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode handshake reply: %v (%s)", err, line)
	}
	var hs handshakeResult
	if err := json.Unmarshal(resp.Result, &hs); err != nil {
		t.Fatalf("decode handshake result: %v (%s)", err, resp.Result)
	}
	if !hs.Ok || hs.HostVersion != "" {
		t.Fatalf("unexpected handshake result: %+v", hs)
	}
}

// TestInferenceUpsertRejectsUnknownFields pins the strict decode: a
// payload written against the previous contract fails loudly instead of
// having its fields silently dropped.
func TestInferenceUpsertRejectsUnknownFields(t *testing.T) {
	m, _ := newTestManager(t)
	m.SetInferenceHandler(InferenceHandler{
		Upsert: func(string, InferenceProfile) error { return nil },
	})
	_, err := m.handleInferenceUpsert(&process{id: "plug"}, rpcRequest{
		Params: json.RawMessage(
			`{"id":"plug-gateway","type":"openai","key_ref":"auth/plug/token"}`,
		),
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("stale payload accepted: %v", err)
	}
}
