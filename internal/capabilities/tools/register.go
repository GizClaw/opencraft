// Package tools registers opencraft's built-in tools as deployable
// tool.Source resources. Each source is a container for one tool group
// (exec, apply_patch, web_fetch); a tool.Assembly aggregates them
// through many "tool" deps, so deployments can add, drop, or override
// groups per layer.
package tools

import (
	"context"
	"errors"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/inference"
	"github.com/GizClaw/flowcraft/core/inference/route"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/workspace"

	"github.com/GizClaw/opencraft/internal/capabilities/agents"
	opmemory "github.com/GizClaw/opencraft/internal/capabilities/memory"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	ocsandbox "github.com/GizClaw/opencraft/internal/capabilities/sandbox"
	"github.com/GizClaw/opencraft/internal/capabilities/sessions"
	skillsvc "github.com/GizClaw/opencraft/internal/capabilities/skills"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/applypatch"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/askuser"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/assembly"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/automation"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/compact"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/exec"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/files"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/imagegen"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/permissions"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/plan"
	plugintools "github.com/GizClaw/opencraft/internal/capabilities/tools/pluginagent"
	plugininstalltools "github.com/GizClaw/opencraft/internal/capabilities/tools/plugininstall"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/remember"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/sessionsearch"
	skillstools "github.com/GizClaw/opencraft/internal/capabilities/tools/skills"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/videogen"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/viewimage"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/webfetch"
	"github.com/GizClaw/opencraft/internal/capabilities/tools/websearch"
	"github.com/GizClaw/opencraft/internal/foundation/platform/shelldetect"
	"github.com/GizClaw/opencraft/internal/foundation/utils/resourcedep"
)

// Register adds every opencraft tool.Source factory to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		r.Register(execSourceFactory{}),
		r.Register(applypatchSourceFactory{}),
		r.Register(webfetchSourceFactory{}),
		r.Register(websearchSourceFactory{}),
		r.Register(askuserSourceFactory{}),
		r.Register(automation.SourceFactory{}),
		r.Register(filesSourceFactory{}),
		r.Register(imagegenSourceFactory{}),
		r.Register(videogenSourceFactory{}),
		r.Register(viewimageSourceFactory{}),
		r.Register(permissionsSourceFactory{}),
		r.Register(planSourceFactory{}),
		r.Register(skillsSourceFactory{}),
		r.Register(sessionsearchSourceFactory{}),
		r.Register(rememberSourceFactory{}),
		r.Register(plugintools.SourceFactory{}),
		r.Register(plugininstalltools.SourceFactory{}),
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
			{Name: "observer", Type: opmemory.UsageObserverResourceKind, Required: false},
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
	var observer func(context.Context, inference.Usage)
	if dep, ok := in.Dep("observer"); ok {
		if obs, ok := dep.(opmemory.UsageObserver); ok {
			observer = obs.ReportUsage
		}
	}
	return toolList{compact.New(router, store, observer)}, nil
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
			// Optional: the escalation prompt needs a user to ask.
			// Headless and embedded runtimes leave it unwired and keep
			// an OS-sandbox refusal as a plain command failure.
			{Name: "escalator", Type: "opencraft.execpolicy", Required: false},
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
	var escalator ocsandbox.Escalator
	if dep, ok := in.Dep("escalator"); ok {
		if esc, ok := dep.(ocsandbox.Escalator); ok {
			escalator = esc
		}
	}
	return execToolList(runner, escalator, goruntime.GOOS), nil
}

// execToolList builds the sandbox-backed exec tools for a target OS.
// exec_session is not offered on Windows: the Windows sandbox runs
// with OS-level write confinement, which the flowcraft backend does
// not combine with ConPTY TTY sessions yet (issue #38).
//
// Only exec_command takes part in escalation: a refusal is detected
// from a finished command's output, and exec_session has no finished
// result to inspect at Start, so a session is never prompted. A
// remembered rule still applies to any spawn, sessions included,
// because the sandbox runner consults it before starting.
func execToolList(
	runner sandbox.Runner,
	escalator ocsandbox.Escalator,
	goos string,
) toolList {
	tools := toolList{exec.MustNewCommand(
		runner,
		exec.WithEscalator(escalator),
		exec.WithShell(shelldetect.Detect(goos)),
	)}
	if ocsandbox.InteractiveSessions(goos) {
		tools = append(tools, exec.MustNewSession(runner))
	}
	return tools
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
					// YOLO sessions disable the sandbox by definition:
					// their web_fetch should not be re-gated at the
					// network layer, so private destinations stay
					// reachable for them too.
					t.SetAllowPrivate(func(ctx context.Context) bool {
						return pol.WebFetch.AllowPrivate ||
							ocsandbox.IsYOLO(ctx, store)
					})
				}
			}
			t.SetGate(gate)
		}
	}
	return toolList{t}, nil
}

// websearchSourceFactory contributes the web_search tool. It needs no
// dependencies: providers and credentials come from the settings, and
// the keyless Parallel/Exa hosted MCP backends make the tool work with
// any model out of the box.
type websearchSourceFactory struct{}

var _ resource.Factory = websearchSourceFactory{}

func (websearchSourceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "tool.Source", Impl: "opencraft/websearch"}
}

func (websearchSourceFactory) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	settings, err := resource.DecodeTyped[websearch.Settings](
		ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	t, err := websearch.New(settings)
	if err != nil {
		return nil, err
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
// viewimageSourceFactory contributes the view_image tool: it reads one
// workspace image and returns it as a multimodal tool result, so a
// vision model can look at it instead of reading bytes.
type viewimageSourceFactory struct{}

var _ resource.Factory = viewimageSourceFactory{}

func (viewimageSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/viewimage",
		Deps: []resource.DepSpec{{
			Name: "hostworkspace", Type: "opencraft.hostworkspace", Required: true,
		}},
	}
}

func (viewimageSourceFactory) New(_ context.Context, in resource.Input) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := resourcedep.Required[workspace.Workspace](
		in, "view_image tool", "hostworkspace")
	if err != nil {
		return nil, err
	}
	settings, err := resource.DecodeTyped[viewimage.Settings](
		context.Background(), in.Settings)
	if err != nil {
		return nil, err
	}
	t, err := viewimage.New(ws, settings)
	if err != nil {
		return nil, err
	}
	return toolList{t}, nil
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
	settings, err := resource.DecodeTyped[imagegen.Settings](
		context.Background(), in.Settings)
	if err != nil {
		return nil, err
	}
	return toolList{imagegen.MustNew(router, ws, settings)}, nil
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
	settings, err := resource.DecodeTyped[videogen.Settings](
		context.Background(), in.Settings)
	if err != nil {
		return nil, err
	}
	return toolList{videogen.MustNew(router, ws, settings)}, nil
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

// sessionsearchSourceFactory contributes the session_search tool: the
// full-text recall over this workspace's archived conversations. The
// store owns the index and the query; the tool owns the envelope.
type sessionsearchSourceFactory struct{}

func (sessionsearchSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/sessionsearch",
		Deps: []resource.DepSpec{
			{Name: "sessions", Type: sessions.ResourceKind, Required: true},
		},
	}
}

func (sessionsearchSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	store, err := resourcedep.Required[*sessions.Store](
		in, "session_search tool", "sessions")
	if err != nil {
		return nil, err
	}
	return toolList{sessionsearch.New(store)}, nil
}

// rememberSourceFactory contributes the remember tool: the model's way
// to store a durable fact in the user-level long-term memory. The tool
// is contributed only when a user database is behind the binding — with
// no store there is nowhere to remember anything.
type rememberSourceFactory struct{}

func (rememberSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/remember",
		Deps: []resource.DepSpec{
			{Name: "usermemory", Type: userstore.ResourceKind, Required: true},
		},
	}
}

func (rememberSourceFactory) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	binding, err := resourcedep.Required[*userstore.Binding](
		in, "remember tool", "usermemory")
	if err != nil {
		return nil, err
	}
	if !binding.Enabled() {
		return toolList{}, nil
	}
	settings, err := resource.DecodeTyped[remember.Settings](ctx, in.Settings)
	if err != nil {
		return nil, err
	}
	built, err := remember.New(ctx, binding.Memory, settings)
	if err != nil {
		return nil, err
	}
	return toolList{built}, nil
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
