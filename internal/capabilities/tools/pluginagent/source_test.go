package pluginagent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/GizClaw/flowcraft/core/tool"

	"github.com/GizClaw/opencraft/internal/capabilities/plugins/agent"
)

type fakeHost struct {
	specs    []agent.ToolSpec
	servers  []agent.MCPServer
	invokeFn func(ctx context.Context, pluginID, method string, args json.RawMessage) (json.RawMessage, error)
}

func (f *fakeHost) ToolSpecs() []agent.ToolSpec { return f.specs }
func (f *fakeHost) MCPServers() []agent.MCPServer {
	return f.servers
}
func (f *fakeHost) Invoke(
	ctx context.Context,
	pluginID, method string,
	args json.RawMessage,
) (json.RawMessage, error) {
	if f.invokeFn != nil {
		return f.invokeFn(ctx, pluginID, method, args)
	}
	return nil, errors.New("not implemented")
}

func TestSourceExposesCapabilityTools(t *testing.T) {
	host := &fakeHost{
		specs: []agent.ToolSpec{{
			PluginID:     "hello",
			Name:         "ping",
			Description:  "Ping the plugin",
			Method:       "ping",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			MutatesState: false,
		}},
		invokeFn: func(
			_ context.Context, pluginID, method string, _ json.RawMessage,
		) (json.RawMessage, error) {
			if pluginID != "hello" || method != "ping" {
				return nil, errors.New("wrong method routed")
			}
			return json.RawMessage(`"pong"`), nil
		},
	}
	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "hello__ping" {
		t.Fatalf("registry names = %v", names)
	}
	got, ok := reg.Get("hello__ping")
	if !ok {
		t.Fatal("hello__ping missing from registry")
	}
	if def := got.Definition(); def.Description != "[plugin hello] Ping the plugin" {
		t.Fatalf("definition = %+v", def)
	}
	res, err := got.Execute(t.Context(), `{"x":1}`)
	if err != nil || res != "pong" {
		t.Fatalf("Execute = (%q, %v)", res, err)
	}
}

func TestSourceEmptyHost(t *testing.T) {
	src, err := newSource(t.Context(), &fakeHost{})
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if len(reg.Names()) != 0 {
		t.Fatalf("empty host must contribute no tools, got %v", reg.Names())
	}
}

func TestSourceSanitizesPluginIDInToolName(t *testing.T) {
	host := &fakeHost{
		specs: []agent.ToolSpec{{
			PluginID: "my.plugin",
			Name:     "ping",
			Method:   "ping",
		}},
	}
	src, err := newSource(t.Context(), host)
	if err != nil {
		t.Fatalf("newSource: %v", err)
	}
	reg, err := tool.NewRegistry([]tool.Source{src})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if names := reg.Names(); len(names) != 1 || names[0] != "my_plugin__ping" {
		t.Fatalf("registry names = %v, want [my_plugin__ping]", names)
	}
}
