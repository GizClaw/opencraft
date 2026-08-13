// Package tools registers opencraft's built-in tools as deployable
// tool.Source resources. Each source is a container for one tool group
// (exec, apply_patch, web_fetch); a tool.Assembly aggregates them
// through many "tool" deps, so deployments can add, drop, or override
// groups per layer.
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/tools/applypatch"
	"github.com/GizClaw/opencraft/internal/tools/execcommand"
	"github.com/GizClaw/opencraft/internal/tools/execsession"
	"github.com/GizClaw/opencraft/internal/tools/webfetch"
)

// Register adds every opencraft tool.Source factory to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		r.Register(execSourceFactory{}),
		r.Register(applypatchSourceFactory{}),
		r.Register(webfetchSourceFactory{}),
	)
}

// execSourceFactory contributes the sandbox-backed exec tools.
type execSourceFactory struct{}

var _ resource.Factory = execSourceFactory{}

func (execSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/exec",
		Deps: []resource.DepSpec{
			{Name: "sandbox", Type: "sandbox.Runner", Required: true},
		},
	}
}

func (execSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	runner, err := requiredDep[sandbox.Runner](in, "sandbox")
	if err != nil {
		return nil, err
	}
	return toolList{
		execcommand.MustNew(runner),
		execsession.MustNew(runner),
	}, nil
}

// applypatchSourceFactory contributes the workspace-backed apply_patch
// tool.
type applypatchSourceFactory struct{}

var _ resource.Factory = applypatchSourceFactory{}

func (applypatchSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/applypatch",
		Deps: []resource.DepSpec{
			{Name: "workspace", Type: "workspace.Workspace", Required: true},
		},
	}
}

func (applypatchSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := requiredDep[workspace.Workspace](in, "workspace")
	if err != nil {
		return nil, err
	}
	return toolList{applypatch.MustNew(ws)}, nil
}

// webfetchSourceFactory contributes the web_fetch tool.
type webfetchSourceFactory struct{}

var _ resource.Factory = webfetchSourceFactory{}

func (webfetchSourceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "tool.Source", Impl: "opencraft/webfetch"}
}

func (webfetchSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	return toolList{webfetch.New()}, nil
}

// toolList adapts a fixed []tool.Tool to tool.Source.
type toolList []tool.Tool

func (l toolList) Tools() []tool.Tool { return l }

func (toolList) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = toolList(nil)

func requiredDep[T any](in resource.Input, name string) (T, error) {
	var zero T
	dep, ok := in.Dep(name)
	if !ok {
		return zero, errdefs.Validationf(
			"tool source: dep %q is required", name)
	}
	value, ok := dep.(T)
	if !ok {
		return zero, errdefs.Validationf(
			"tool source: dep %q is %T, want %T", name, dep, zero)
	}
	return value, nil
}

// sourceEnabled reads the optional enabled switch from a source's
// settings. Absent means enabled.
func sourceEnabled(in resource.Input) bool {
	if len(in.Settings) == 0 {
		return true
	}
	var s struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(in.Settings, &s); err != nil || s.Enabled == nil {
		return true
	}
	return *s.Enabled
}
