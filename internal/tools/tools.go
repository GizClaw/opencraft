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
	"github.com/GizClaw/opencraft/internal/tools/askuser"
	"github.com/GizClaw/opencraft/internal/tools/execcommand"
	"github.com/GizClaw/opencraft/internal/tools/execsession"
	"github.com/GizClaw/opencraft/internal/tools/files"
	"github.com/GizClaw/opencraft/internal/tools/plan"
	"github.com/GizClaw/opencraft/internal/tools/requestpermissions"
	"github.com/GizClaw/opencraft/internal/tools/webfetch"
	"github.com/GizClaw/opencraft/internal/utils/resourcedep"
)

// Register adds every opencraft tool.Source factory to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		r.Register(execSourceFactory{}),
		r.Register(applypatchSourceFactory{}),
		r.Register(webfetchSourceFactory{}),
		r.Register(askuserSourceFactory{}),
		r.Register(filesSourceFactory{}),
		r.Register(requestpermissionsSourceFactory{}),
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
	runner, err := resourcedep.Required[sandbox.Runner](in, "tool source", "sandbox")
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
	ws, err := resourcedep.Required[workspace.Workspace](in, "tool source", "workspace")
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

// askuserSourceFactory contributes the ask_user tool. It needs no
// sandbox/workspace: the host is recovered from the tool context.
type askuserSourceFactory struct{}

var _ resource.Factory = askuserSourceFactory{}

func (askuserSourceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "tool.Source", Impl: "opencraft/askuser"}
}

func (askuserSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	return toolList{askuser.New()}, nil
}

// filesSourceFactory contributes the workspace-backed file tools.
type filesSourceFactory struct{}

var _ resource.Factory = filesSourceFactory{}

func (filesSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/files",
		Deps: []resource.DepSpec{
			{Name: "workspace", Type: "workspace.Workspace", Required: true},
		},
	}
}

func (filesSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := resourcedep.Required[workspace.Workspace](in, "tool source", "workspace")
	if err != nil {
		return nil, err
	}
	return toolList(files.MustNew(ws).Tools()), nil
}

// requestpermissionsSourceFactory contributes the request_permissions
// tool; the exec policy is resolved from the turn host at call time.
type requestpermissionsSourceFactory struct{}

var _ resource.Factory = requestpermissionsSourceFactory{}

func (requestpermissionsSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/requestpermissions",
	}
}

func (requestpermissionsSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	return toolList{requestpermissions.New()}, nil
}

// PlanSourceFactory contributes the update_plan tool over a
// runtime-scoped store.
type PlanSourceFactory struct {
	store *plan.Store
}

// NewPlanSourceFactory returns a tool.Source factory for the
// update_plan tool. store must not be nil.
func NewPlanSourceFactory(store *plan.Store) resource.Factory {
	return PlanSourceFactory{store: store}
}

func (PlanSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/plan",
	}
}

func (f PlanSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	if f.store == nil {
		return nil, errdefs.Validationf(
			"update_plan tool resource: store is required")
	}
	return toolList(plan.MustNew(f.store).Tools()), nil
}

// toolList adapts a fixed []tool.Tool to tool.Source.
type toolList []tool.Tool

func (l toolList) Tools() []tool.Tool { return l }

func (toolList) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = toolList(nil)

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
