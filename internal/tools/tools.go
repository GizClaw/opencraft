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

	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/tool"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/agents"
	ocsandbox "github.com/GizClaw/opencraft/internal/sandbox"
	"github.com/GizClaw/opencraft/internal/sessions"
	skillsvc "github.com/GizClaw/opencraft/internal/skills"
	"github.com/GizClaw/opencraft/internal/tools/applypatch"
	"github.com/GizClaw/opencraft/internal/tools/askuser"
	"github.com/GizClaw/opencraft/internal/tools/assembly"
	"github.com/GizClaw/opencraft/internal/tools/compact"
	"github.com/GizClaw/opencraft/internal/tools/exec"
	"github.com/GizClaw/opencraft/internal/tools/files"
	"github.com/GizClaw/opencraft/internal/tools/imagegen"
	"github.com/GizClaw/opencraft/internal/tools/permissions"
	"github.com/GizClaw/opencraft/internal/tools/plan"
	skillstools "github.com/GizClaw/opencraft/internal/tools/skills"
	"github.com/GizClaw/opencraft/internal/tools/videogen"
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
		r.Register(imagegenSourceFactory{}),
		r.Register(videogenSourceFactory{}),
		r.Register(permissionsSourceFactory{}),
		r.Register(planSourceFactory{}),
		r.Register(skillsSourceFactory{}),
		r.Register(agentlifecycleSourceFactory{}),
		r.Register(compactSourceFactory{}),
		r.Register(assembly.AssemblyFactory{}),
	)
}

// compactSourceFactory contributes the internal compact tool. It needs
// the router for LLM condensation and the session store for the
// per-conversation compaction artifact.
type compactSourceFactory struct{}

var _ resource.Factory = compactSourceFactory{}

func (compactSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/compact",
		Deps: []resource.DepSpec{
			{Name: "router", Type: "inference.Router", Required: true},
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

func (compactSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := resourcedep.Required[*route.Router](
		in, "compact tool", "router")
	if err != nil {
		return nil, err
	}
	store, err := resourcedep.Required[*sessions.Store](
		in, "compact tool", "sessions")
	if err != nil {
		return nil, err
	}
	return toolList{compact.New(router, store)}, nil
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
		exec.MustNewCommand(runner),
		exec.MustNewSession(runner),
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
			{Name: "hostworkspace", Type: "opencraft.hostworkspace", Required: true},
		},
	}
}

func (applypatchSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := resourcedep.Required[workspace.Workspace](
		in, "tool source", "hostworkspace")
	if err != nil {
		return nil, err
	}
	return toolList{applypatch.MustNew(ws)}, nil
}

// webfetchSourceFactory contributes the web_fetch tool.
type webfetchSourceFactory struct{}

var _ resource.Factory = webfetchSourceFactory{}

func (webfetchSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/webfetch",
		Deps: []resource.DepSpec{
			{Name: "netpolicy", Type: ocsandbox.NetPolicyResourceKind, Required: false},
			{Name: "sessions", Type: sessions.ResourceKind, Required: false},
		},
	}
}

func (webfetchSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	t := webfetch.New()
	if dep, ok := in.Dep("netpolicy"); ok {
		if pol, ok := dep.(ocsandbox.Policy); ok {
			gate := webfetch.DomainGate(pol.WebFetch)
			if dep, ok := in.Dep("sessions"); ok {
				store, isStore := dep.(*sessions.Store)
				if isStore && store != nil {
					gate = webfetch.YOLOBypassGate(store, gate)
				}
			}
			t.SetGate(gate)
		}
	}
	return toolList{t}, nil
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
			{Name: "hostworkspace", Type: "opencraft.hostworkspace", Required: true},
		},
	}
}

func (filesSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := resourcedep.Required[workspace.Workspace](
		in, "tool source", "hostworkspace")
	if err != nil {
		return nil, err
	}
	return toolList(files.MustNew(ws).Tools()), nil
}

// imagegenSourceFactory contributes the generate_image tool. It needs
// the router (image-capable model selection/fallback) and the host
// workspace (generated files land under generated/).
type imagegenSourceFactory struct{}

var _ resource.Factory = imagegenSourceFactory{}

func (imagegenSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/imagegen",
		Deps: []resource.DepSpec{
			{Name: "router", Type: "inference.Router", Required: true},
			{Name: "hostworkspace", Type: "opencraft.hostworkspace", Required: true},
		},
	}
}

func (imagegenSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := resourcedep.Required[*route.Router](
		in, "imagegen tool", "router")
	if err != nil {
		return nil, err
	}
	ws, err := resourcedep.Required[workspace.Workspace](
		in, "imagegen tool", "hostworkspace")
	if err != nil {
		return nil, err
	}
	return toolList{imagegen.MustNew(router, ws)}, nil
}

// videogenSourceFactory contributes the generate_video tool. It needs
// the router (video-capable model selection/fallback) and the host
// workspace (generated files land under generated/).
type videogenSourceFactory struct{}

var _ resource.Factory = videogenSourceFactory{}

func (videogenSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/videogen",
		Deps: []resource.DepSpec{
			{Name: "router", Type: "inference.Router", Required: true},
			{Name: "hostworkspace", Type: "opencraft.hostworkspace", Required: true},
		},
	}
}

func (videogenSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := resourcedep.Required[*route.Router](
		in, "videogen tool", "router")
	if err != nil {
		return nil, err
	}
	ws, err := resourcedep.Required[workspace.Workspace](
		in, "videogen tool", "hostworkspace")
	if err != nil {
		return nil, err
	}
	return toolList{videogen.MustNew(router, ws)}, nil
}

// permissionsSourceFactory contributes the request_permissions tool
// over the runtime execpolicy resource.
type permissionsSourceFactory struct{}

var _ resource.Factory = permissionsSourceFactory{}

func (permissionsSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/permissions",
		Deps: []resource.DepSpec{
			{Name: "execpolicy", Type: "opencraft.execpolicy", Required: true},
		},
	}
}

func (permissionsSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	policy, err := resourcedep.Required[permissions.Policy](
		in, "permissions", "execpolicy")
	if err != nil {
		return nil, err
	}
	return toolList{permissions.New(policy)}, nil
}

// planSourceFactory contributes the update_plan tool over the session
// store (plan persistence lives with the session's other state).
type planSourceFactory struct{}

func (planSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/plan",
		Deps: []resource.DepSpec{
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

func (planSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	store, err := resourcedep.Required[*sessions.Store](
		in, "update_plan tool", "sessions")
	if err != nil {
		return nil, err
	}
	return toolList(plan.MustNew(plan.NewStore(store)).Tools()), nil
}

// skillsSourceFactory contributes the skill_search / skill_read tools
// over the shared skills registry.
type skillsSourceFactory struct{}

var _ resource.Factory = skillsSourceFactory{}

func (skillsSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/skills",
		Deps: []resource.DepSpec{
			{Name: "skills", Type: skillsvc.ResourceKind, Required: true},
		},
	}
}

func (skillsSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	svc, err := resourcedep.Required[*skillsvc.Service](
		in, "tool source", "skills")
	if err != nil {
		return nil, err
	}
	return toolList(skillstools.MustNew(svc).Tools()), nil
}

// agentlifecycleSourceFactory contributes the create_agent /
// unregister_agent tools over the persistent subagent registry.
type agentlifecycleSourceFactory struct{}

var _ resource.Factory = agentlifecycleSourceFactory{}

func (agentlifecycleSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/agentlifecycle",
		Deps: []resource.DepSpec{
			{Name: "agentlifecycle", Type: agents.ResourceKind, Required: true},
		},
	}
}

func (agentlifecycleSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	lifecycle, err := resourcedep.Required[*agents.Lifecycle](
		in, "tool source", "agentlifecycle")
	if err != nil {
		return nil, err
	}
	return toolList(agents.MustNew(lifecycle).Tools()), nil
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
