package config

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"

	"github.com/GizClaw/opencraft/internal/capabilities/tools/files"
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

// TestEmbeddedToolsReadWindowFitsTruncateCap pins the relationship
// between the read tool's window and the result policy above it: a
// window larger than middlewares.truncate.max_chars is cut a second
// time after the tool already chose a range, and what the model gets
// back is the two ends of a range instead of the range. The budget
// leaves room for the JSON envelope and the other text in the result.
func TestEmbeddedToolsReadWindowFitsTruncateCap(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var toolsSettings struct {
		Middlewares struct {
			Truncate struct {
				Enabled  bool `json:"enabled"`
				MaxChars int  `json:"max_chars"`
			} `json:"truncate"`
		} `json:"middlewares"`
	}
	if err := json.Unmarshal(
		view.Document.Resources["tools"].Settings, &toolsSettings,
	); err != nil {
		t.Fatalf("decode tools settings: %v", err)
	}
	truncate := toolsSettings.Middlewares.Truncate
	if !truncate.Enabled || truncate.MaxChars <= 0 {
		t.Fatalf("tools assembly declares no truncate cap: %+v", truncate)
	}
	if files.ReadResultChars >= truncate.MaxChars {
		t.Fatalf("read window %d leaves no room under the %d-char cap; "+
			"a read would be excerpted after the tool chose its range",
			files.ReadResultChars, truncate.MaxChars)
	}
	// The envelope carries file_path, offset, limit, total_lines,
	// is_truncated, next_offset, escaping, and the truncate middleware
	// also counts the other text in the result: a budget within a
	// couple of characters of the cap is not headroom.
	if headroom := truncate.MaxChars - files.ReadResultChars; headroom < 1024 {
		t.Fatalf("read window %d leaves only %d chars under the %d-char cap",
			files.ReadResultChars, headroom, truncate.MaxChars)
	}
}

// TestEmbeddedToolsSetTheToolTimeout pins the deploy's generic per-call
// deadline: the assembly builds the timeout middleware by hand so it can
// read each tool's SelfTimeout claim, and it only does so when the
// section names a default. A renamed key or a lost indent then leaves
// every tool without a bound while each package test still passes, which
// is what the rollout corpus caught once already.
func TestEmbeddedToolsSetTheToolTimeout(t *testing.T) {
	userDir := t.TempDir()
	mgr, err := Open(Options{UserDir: userDir})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var toolsSettings struct {
		Middlewares struct {
			Timeout struct {
				Default string `json:"default"`
			} `json:"timeout"`
		} `json:"middlewares"`
	}
	if err := json.Unmarshal(
		view.Document.Resources["tools"].Settings, &toolsSettings,
	); err != nil {
		t.Fatalf("decode tools settings: %v", err)
	}
	spec := toolsSettings.Middlewares.Timeout.Default
	if spec == "" {
		t.Fatal("tools assembly declares no middlewares.timeout.default: " +
			"every tool that does not bound itself is unbounded")
	}
	d, err := time.ParseDuration(spec)
	if err != nil {
		t.Fatalf("middlewares.timeout.default %q does not parse: %v", spec, err)
	}
	if d <= 0 {
		t.Fatalf("middlewares.timeout.default %q is not a positive bound", spec)
	}
}

// TestEmbeddedToolsWireDelegateMode pins the two deploy dependencies a
// subagent's permission mode travels through, because both fail
// silently: a tool assembly without the session store stamps no mode on
// the delegate call, and a delegation service without it writes none to
// the child session. Either way the subagent quietly runs confined under
// its caller, which is exactly the bug this wiring exists to prevent —
// and every factory test still passes, because they inject the store by
// hand.
func TestEmbeddedToolsWireDelegateMode(t *testing.T) {
	mgr, err := Open(Options{UserDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store, ok := view.Document.Resources["sessions"]
	if !ok {
		t.Fatal("the session store resource is not declared")
	}
	if store.Kind != "session.Store" {
		t.Fatalf("sessions kind = %q, want session.Store", store.Kind)
	}
	for name, want := range map[string]string{
		// Stamping side: the assembly reads the caller's mode.
		"tools": "sessions",
		// Applying side: the service writes it to the child session.
		"delegate": "sessions",
	} {
		res, ok := view.Document.Resources[name]
		if !ok {
			t.Fatalf("resource %q is not declared", name)
		}
		if got := string(res.Deps["sessions"]); got != want {
			t.Fatalf("%s deps[sessions] = %q, want %q (all deps: %+v)",
				name, got, want, res.Deps)
		}
	}
}
