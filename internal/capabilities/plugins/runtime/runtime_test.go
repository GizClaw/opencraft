package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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

// oversizedPlugin emits a line beyond bufio.Scanner's token limit and
// exits, exercising the read-loop teardown path.
func oversizedPlugin() {
	_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", 128<<10))
	os.Exit(0)
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
	}
}

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
	m.callTimeout = 2 * time.Second
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
	m.handshakeTimeout = 2 * time.Second
	m.callTimeout = 2 * time.Second
	return m, sec
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
	var upsertedSpec map[string]any
	var removedPlugin, removedID string
	m.SetInferenceHandler(InferenceHandler{
		Upsert: func(pluginID string, profile InferenceProfile) error {
			upsertedPlugin = pluginID
			upsertedID = profile.ID
			upsertedSpec = profile.ProviderSpec
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
			"id": "plug-gateway",
			"provider_spec": {
				"chat_stream_options": {"include_usage": false}
			}
		}`),
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if upsertedPlugin != "plug" || upsertedID != "plug-gateway" {
		t.Fatalf("upsert forwarded %q/%q", upsertedPlugin, upsertedID)
	}
	opts, ok := upsertedSpec["chat_stream_options"].(map[string]any)
	if !ok || opts["include_usage"] != false {
		t.Fatalf("upsert provider_spec = %#v", upsertedSpec)
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
	m, _ := newTestManager(t)
	log := &lockedBuffer{}
	m.SetLogger(log)
	if _, err := m.Invoke(context.Background(), "test-plugin", "auth.poll", nil); err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if err := m.Cleanup("test-plugin"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(log.String(), "CLEANUP_CALLED") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !strings.Contains(log.String(), "CLEANUP_CALLED") {
		t.Fatalf("plugin cleanup callback not invoked; stderr=%q", log.String())
	}
	m.mu.Lock()
	_, running := m.procs["test-plugin"]
	m.mu.Unlock()
	if running {
		t.Fatal("process still registered after Cleanup")
	}
}

// lockedBuffer is a concurrency-safe writer for the stderr forwarder.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
