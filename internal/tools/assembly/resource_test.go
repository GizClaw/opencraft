package assembly

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/hooks"
)

const testSecret = "sk-1234567890abcdef"

// stubSource adapts a single tool to tool.Source.
type stubSource struct {
	t tool.Tool
}

func (s stubSource) Tools() []tool.Tool         { return []tool.Tool{s.t} }
func (s stubSource) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = stubSource{}

func newAssembly(t *testing.T, settings string, src tool.Source) *tool.Assembly {
	t.Helper()
	f := AssemblyFactory{}
	value, err := f.New(context.Background(), resource.Input{
		Settings: []byte(settings),
		Deps:     map[string]any{"tool": src},
	})
	if err != nil {
		t.Fatalf("assembly factory: %v", err)
	}
	asm, ok := value.(*tool.Assembly)
	if !ok {
		t.Fatalf("factory returned %T, want *tool.Assembly", value)
	}
	return asm
}

func probeTool(content string) tool.Tool {
	return tool.FuncTool(
		message.ToolDefinition{Name: "probe"},
		func(_ context.Context, _ string) (string, error) {
			return content, nil
		},
	)
}

func runProbe(asm *tool.Assembly) message.ToolResult {
	return asm.Execute(context.Background(), message.ToolCall{
		ID:        "call-1",
		Name:      "probe",
		Arguments: json.RawMessage(`{"msg":"hello sk-1234567890abcdef"}`),
	})
}

func readAudit(t *testing.T, dir string) auditEntry {
	t.Helper()
	path := filepath.Join(dir, "tool-calls.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var entry auditEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode audit record %q: %v", raw, err)
	}
	return entry
}

func TestAssemblyWiresResultLimitRedactAudit(t *testing.T) {
	auditDir := filepath.Join(t.TempDir(), "audit")
	settings := `{
		"middlewares": {
			"recover": {"enabled": true},
			"timeout": {"default": "5m"},
			"result_limit": {"max_chars": 300},
			"redact": {
				"enabled": true,
				"rules": [{"pattern": "sk-[A-Za-z0-9]{16,}"}]
			},
			"audit": {"enabled": true, "dir": "` + auditDir + `"}
		}
	}`
	asm := newAssembly(t, settings, stubSource{
		t: probeTool(strings.Repeat("x", 500) + " secret: " + testSecret),
	})

	res := runProbe(asm)
	if len([]rune(res.Content)) > 300 {
		t.Fatalf("result has %d runes, want <= 300", len([]rune(res.Content)))
	}
	if !strings.Contains(res.Content, "result truncated") {
		t.Fatalf("limiter marker missing: %q", res.Content)
	}
	if strings.Contains(res.Content, testSecret) {
		t.Fatalf("model-facing result leaked secret: %q", res.Content)
	}

	entry := readAudit(t, auditDir)
	if entry.Tool != "probe" || entry.CallID != "call-1" {
		t.Fatalf("audit record = %+v, want probe/call-1", entry)
	}
	if strings.Contains(entry.Result, testSecret) {
		t.Fatalf("audit result leaked secret: %q", entry.Result)
	}
	if strings.Contains(entry.Arguments, testSecret) {
		t.Fatalf("audit arguments leaked secret: %q", entry.Arguments)
	}
	if !strings.Contains(entry.Result, "result truncated") {
		t.Fatalf("audit should record the final limited content: %q", entry.Result)
	}
	if entry.DurationMS < 0 {
		t.Fatalf("audit duration = %d, want >= 0", entry.DurationMS)
	}
}

func TestAssemblyAuditRedactsEvenWhenRedactDisabled(t *testing.T) {
	auditDir := filepath.Join(t.TempDir(), "audit")
	settings := `{
		"middlewares": {
			"redact": {
				"enabled": false,
				"rules": [{"pattern": "sk-[A-Za-z0-9]{16,}"}]
			},
			"audit": {"enabled": true, "dir": "` + auditDir + `"}
		}
	}`
	asm := newAssembly(t, settings, stubSource{
		t: probeTool("result: " + testSecret),
	})

	res := runProbe(asm)
	if !strings.Contains(res.Content, testSecret) {
		t.Fatalf("redact disabled: model should see the secret, got %q", res.Content)
	}
	entry := readAudit(t, auditDir)
	if strings.Contains(entry.Result, testSecret) {
		t.Fatalf("audit must stay redacted even with redact disabled: %q", entry.Result)
	}
}

func TestAssemblyResultLimitZeroDisabled(t *testing.T) {
	settings := `{
		"middlewares": {
			"result_limit": {"max_chars": 0}
		}
	}`
	content := strings.Repeat("x", 5000)
	asm := newAssembly(t, settings, stubSource{t: probeTool(content)})
	if res := runProbe(asm); res.Content != content {
		t.Fatalf("zero limit must pass through, got %d runes", len([]rune(res.Content)))
	}
}

func TestAssemblyTruncatePersistsRedactedContent(t *testing.T) {
	work := t.TempDir()
	cacheDir := filepath.Join(work, ".opencraft", "cache", "tools")
	auditDir := filepath.Join(work, "audit")
	settings := `{
		"middlewares": {
			"result_limit": {"max_chars": 1000},
			"redact": {
				"enabled": true,
				"rules": [{"pattern": "sk-[A-Za-z0-9]{16,}"}]
			},
			"truncate": {
				"enabled": true,
				"max_chars": 200,
				"dir": "` + cacheDir + `",
				"work_dir": "` + work + `"
			},
			"audit": {"enabled": true, "dir": "` + auditDir + `"}
		}
	}`
	asm := newAssembly(t, settings, stubSource{
		t: probeTool(strings.Repeat("x", 800) + " secret: " + testSecret),
	})

	res := runProbe(asm)
	if len([]rune(res.Content)) > 200 {
		t.Fatalf("truncated result = %d runes, want <= 200", len([]rune(res.Content)))
	}
	if strings.Contains(res.Content, testSecret) {
		t.Fatalf("truncated result leaked secret: %q", res.Content)
	}
	if !strings.Contains(res.Content, "truncated; full output:") {
		t.Fatalf("truncate marker missing: %q", res.Content)
	}
	raw, err := os.ReadFile(filepath.Join(cacheDir, "call-1.output"))
	if err != nil {
		t.Fatalf("full output not persisted: %v", err)
	}
	if strings.Contains(string(raw), testSecret) {
		t.Fatalf("persisted cache leaked secret")
	}
	if entry := readAudit(t, auditDir); strings.Contains(entry.Result, testSecret) {
		t.Fatalf("audit leaked secret: %q", entry.Result)
	}
}

func TestAssemblyRejectsInvalidMiddlewareSettings(t *testing.T) {
	cases := []struct {
		name     string
		settings string
	}{
		{
			name: "invalid redact pattern",
			settings: `{
				"middlewares": {
					"redact": {"enabled": true, "rules": [{"pattern": "("}]}
				}
			}`,
		},
		{
			name: "empty redact pattern",
			settings: `{
				"middlewares": {
					"redact": {"enabled": true, "rules": [{"pattern": ""}]}
				}
			}`,
		},
		{
			name: "negative result limit",
			settings: `{
				"middlewares": {
					"result_limit": {"max_chars": -1}
				}
			}`,
		},
		{
			name: "audit without dir",
			settings: `{
				"middlewares": {
					"audit": {"enabled": true}
				}
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := AssemblyFactory{}
			_, err := f.New(context.Background(), resource.Input{
				Settings: []byte(tc.settings),
				Deps:     map[string]any{"tool": stubSource{t: probeTool("x")}},
			})
			if err == nil {
				t.Fatal("factory must reject invalid middleware settings")
			}
		})
	}
}

func TestAssemblyFiresToolHooks(t *testing.T) {
	dir := t.TempDir()
	hookOut := filepath.Join(dir, "hook.out")
	cfg := fmt.Sprintf(`{
		"hooks": {
			"PreToolUse":  [{"matcher": "*", "hooks": [{"command": "echo pre >> %s"}]}],
			"PostToolUse": [{"hooks": [{"command": "echo post >> %s"}]}]
		}
	}`, hookOut, hookOut)
	path := filepath.Join(dir, "hooks.json")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	mgr, err := hooks.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	f := AssemblyFactory{}
	value, err := f.New(context.Background(), resource.Input{
		Settings: []byte(`{"middlewares": {}}`),
		Deps: map[string]any{
			"tool":  stubSource{t: probeTool("ok")},
			"hooks": mgr,
		},
	})
	if err != nil {
		t.Fatalf("assembly factory: %v", err)
	}
	asm := value.(*tool.Assembly)
	runProbe(asm)

	data, err := os.ReadFile(hookOut)
	if err != nil {
		t.Fatalf("hooks did not run: %v", err)
	}
	if !strings.Contains(string(data), "pre") || !strings.Contains(string(data), "post") {
		t.Fatalf("hook output = %q, want pre and post", data)
	}
}
