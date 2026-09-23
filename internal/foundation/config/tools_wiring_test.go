package config

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"
)

// TestEmbeddedToolsWireSessionSearch pins the deploy wiring: the source
// has to exist and be listed in the tools assembly, and it must stay
// out of dynamic.exposures so discovery happens through tool_search
// (recall is an occasional need, not a permanent prompt slot).
func TestEmbeddedToolsWireSessionSearch(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, ok := view.Document.Resources["tool.sessionsearch"]
	if !ok {
		t.Fatal("tool.sessionsearch source is not declared")
	}
	if source.Impl != "opencraft/sessionsearch" {
		t.Fatalf("tool.sessionsearch impl = %q", source.Impl)
	}
	if source.Deps["sessions"] != "sessions" {
		t.Fatalf("tool.sessionsearch deps = %+v", source.Deps)
	}
	tools, ok := view.Document.Resources["tools"]
	if !ok {
		t.Fatal("tools assembly is not declared")
	}
	if tools.Deps["tool.sessionsearch"] != "tool.sessionsearch" {
		t.Fatalf("tools assembly does not include tool.sessionsearch: %+v",
			tools.Deps)
	}
	var settings struct {
		Dynamic struct {
			Exposures map[string]string `json:"exposures"`
		} `json:"dynamic"`
	}
	if err := json.Unmarshal(tools.Settings, &settings); err != nil {
		t.Fatalf("decode tools settings: %v", err)
	}
	if mode, ok := settings.Dynamic.Exposures["session_search"]; ok {
		t.Fatalf("session_search must stay deferred, found exposure %q", mode)
	}
}

// TestEmbeddedToolsWireRemember pins the remember deploy wiring and the
// one thing the YAML anchor is there for: the tool refuses exactly the
// rules the result middleware applies, so what is not allowed to leave
// the runtime is also not allowed to become an injected memory.
func TestEmbeddedToolsWireRemember(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, ok := view.Document.Resources["tool.remember"]
	if !ok {
		t.Fatal("tool.remember source is not declared")
	}
	if source.Impl != "opencraft/remember" {
		t.Fatalf("tool.remember impl = %q", source.Impl)
	}
	if source.Deps["usermemory"] != "usermemory" {
		t.Fatalf("tool.remember deps = %+v", source.Deps)
	}
	tools, ok := view.Document.Resources["tools"]
	if !ok {
		t.Fatal("tools assembly is not declared")
	}
	if tools.Deps["tool.remember"] != "tool.remember" {
		t.Fatalf("tools assembly does not include tool.remember: %+v",
			tools.Deps)
	}

	type redactSettings struct {
		Enabled bool `json:"enabled"`
		Rules   []struct {
			Pattern string `json:"pattern"`
		} `json:"rules"`
	}
	var rememberSettings struct {
		WorkDir       string         `json:"work_dir"`
		MaxOperations int            `json:"max_operations"`
		MaxTextChars  int            `json:"max_text_chars"`
		Redact        redactSettings `json:"redact"`
	}
	if err := json.Unmarshal(source.Settings, &rememberSettings); err != nil {
		t.Fatalf("decode tool.remember settings: %v", err)
	}
	if rememberSettings.WorkDir == "" {
		t.Fatal("tool.remember declares no work_dir")
	}
	if rememberSettings.MaxOperations <= 0 || rememberSettings.MaxTextChars <= 0 {
		t.Fatalf("tool.remember bounds = %+v",
			rememberSettings)
	}
	var toolsSettings struct {
		Middlewares struct {
			Redact redactSettings `json:"redact"`
		} `json:"middlewares"`
	}
	if err := json.Unmarshal(tools.Settings, &toolsSettings); err != nil {
		t.Fatalf("decode tools settings: %v", err)
	}
	if !rememberSettings.Redact.Enabled {
		t.Fatal("tool.remember must apply the secret rules on write")
	}
	if len(rememberSettings.Redact.Rules) == 0 {
		t.Fatal("tool.remember declares no secret rules")
	}
	if len(rememberSettings.Redact.Rules) !=
		len(toolsSettings.Middlewares.Redact.Rules) {
		t.Fatalf("secret rules drifted: tool.remember has %d, the "+
			"middleware has %d", len(rememberSettings.Redact.Rules),
			len(toolsSettings.Middlewares.Redact.Rules))
	}
	for i, rule := range rememberSettings.Redact.Rules {
		if rule.Pattern != toolsSettings.Middlewares.Redact.Rules[i].Pattern {
			t.Fatalf("secret rule %d drifted: %q vs %q", i,
				rule.Pattern, toolsSettings.Middlewares.Redact.Rules[i].Pattern)
		}
	}
}

// TestEmbeddedToolsWireWebSearch pins the web_search deploy wiring: the
// source has to exist, stay enabled by default (the keyless hosted MCP
// backends need no configuration), and be listed in the tools assembly
// or the model never sees it.
func TestEmbeddedToolsWireWebSearch(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, ok := view.Document.Resources["tool.websearch"]
	if !ok {
		t.Fatal("tool.websearch source is not declared")
	}
	if source.Impl != "opencraft/websearch" {
		t.Fatalf("tool.websearch impl = %q", source.Impl)
	}
	tools, ok := view.Document.Resources["tools"]
	if !ok {
		t.Fatal("tools assembly is not declared")
	}
	if tools.Deps["tool.websearch"] != "tool.websearch" {
		t.Fatalf("tools assembly does not include tool.websearch: %+v",
			tools.Deps)
	}
}

// TestEmbeddedToolsWireViewImage pins the deploy wiring: the tool has
// to be both declared as a source and listed in the tools assembly, or
// the model never sees it.
func TestEmbeddedToolsWireViewImage(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	source, ok := view.Document.Resources["tool.viewimage"]
	if !ok {
		t.Fatal("tool.viewimage source is not declared")
	}
	if source.Impl != "opencraft/viewimage" {
		t.Fatalf("tool.viewimage impl = %q", source.Impl)
	}
	if source.Deps["hostworkspace"] != "hostws" {
		t.Fatalf("tool.viewimage deps = %+v", source.Deps)
	}
	tools, ok := view.Document.Resources["tools"]
	if !ok {
		t.Fatal("tools assembly is not declared")
	}
	if tools.Deps["tool.viewimage"] != "tool.viewimage" {
		t.Fatalf("tools assembly does not include tool.viewimage: %+v", tools.Deps)
	}
}

// TestEmbeddedToolsViewImageFitsPartBudget pins the unit conversion
// between the two embedded numbers: result_limit meters non-text parts
// on the canonical wire encoding (message.MarshalPart), where an inline
// image spends base64-expanded bytes plus a small JSON envelope, while
// view_image's max_bytes counts raw JPEG bytes. A raw target that only
// looks equal to the budget makes the middleware drop every image the
// tool just produced.
func TestEmbeddedToolsViewImageFitsPartBudget(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var imageSettings struct {
		MaxBytes int `json:"max_bytes"`
	}
	if err := json.Unmarshal(
		view.Document.Resources["tool.viewimage"].Settings,
		&imageSettings,
	); err != nil {
		t.Fatalf("decode tool.viewimage settings: %v", err)
	}
	if imageSettings.MaxBytes <= 0 {
		t.Fatalf("tool.viewimage declares no max_bytes: %+v", imageSettings)
	}

	var toolsSettings struct {
		Middlewares struct {
			ResultLimit struct {
				PartBudgetBytes *int `json:"part_budget_bytes"`
			} `json:"result_limit"`
		} `json:"middlewares"`
	}
	if err := json.Unmarshal(
		view.Document.Resources["tools"].Settings, &toolsSettings,
	); err != nil {
		t.Fatalf("decode tools settings: %v", err)
	}
	budget := toolsSettings.Middlewares.ResultLimit.PartBudgetBytes
	if budget == nil {
		t.Fatal("tools assembly declares no result_limit.part_budget_bytes")
	}

	source, err := media.NewImageBytes(
		make([]byte, imageSettings.MaxBytes), "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	wire, err := message.MarshalPart(message.ImagePart{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) > *budget {
		t.Fatalf(
			"view_image max_bytes %d marshals to %d bytes, over the %d-byte part budget",
			imageSettings.MaxBytes, len(wire), *budget,
		)
	}
}
