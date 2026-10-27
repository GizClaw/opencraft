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
	"github.com/GizClaw/flowcraft/core/telemetry"
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
)

// Register adds every opencraft tool.Source factory to r.
func Register(r *resource.Registry) error {
	return errors.Join(
		resource.Register(r, execSourceFactory{}),
		resource.Register(r, applypatchSourceFactory{}),
		resource.Register(r, webfetchSourceFactory{}),
		resource.Register(r, websearchSourceFactory{}),
		resource.Register(r, askuserSourceFactory{}),
		resource.Register(r, automation.SourceFactory{}),
		resource.Register(r, filesSourceFactory{}),
		resource.Register(r, imagegenSourceFactory{}),
		resource.Register(r, videogenSourceFactory{}),
		resource.Register(r, viewimageSourceFactory{}),
		resource.Register(r, permissionsSourceFactory{}),
		resource.Register(r, planSourceFactory{}),
		resource.Register(r, skillsSourceFactory{}),
		resource.Register(r, sessionsearchSourceFactory{}),
		resource.Register(r, rememberSourceFactory{}),
		resource.Register(r, plugintools.SourceFactory{}),
		resource.Register(r, plugininstalltools.SourceFactory{}),
		resource.Register(r, agentlifecycleSourceFactory{}),
		resource.Register(r, compactSourceFactory{}),
		resource.Register(r, assembly.AssemblyFactory{}),
	)
}

// compactSourceFactory contributes the internal compact tool. It needs
// the router for LLM condensation and the session store for the
// per-conversation compaction artifact.
type compactSourceFactory struct{}

// Dependency handles. A tool source declares a dep and reads it through
// the same handle, so the assembly layer type-checks every edge before a
// resource is built.
var (
	depRouter        = resource.Require[*route.Router]("router")
	depSessions      = resource.Require[*sessions.Store]("sessions")
	depSandbox       = resource.Require[sandbox.Runner]("sandbox")
	depHostWorkspace = resource.Require[workspace.Workspace]("hostworkspace")
	depExecPolicy    = resource.Require[permissions.Policy]("execpolicy")
	depUserMemory    = resource.Require[*userstore.Binding]("usermemory")
	depSkills        = resource.Require[*skillsvc.Service]("skills")
	depAgentLife     = resource.Require[*agents.Lifecycle]("agentlifecycle")

	depObserver     = resource.Optional[opmemory.UsageObserver]("observer")
	depOptSessions  = resource.Optional[*sessions.Store]("sessions")
	depOptNetPolicy = resource.Optional[ocsandbox.Policy]("netpolicy")
)

var _ resource.Factory[toolList] = compactSourceFactory{}

func (compactSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/compact",
		Deps: []resource.Requirement{
			depRouter.Spec(), depObserver.Spec(),
		},
	}
}

func (compactSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := depRouter.Get(in)
	if err != nil {
		return nil, err
	}
	var observer func(context.Context, inference.Usage)
	if obs, ok, err := depObserver.TryGet(in); err != nil {
		return toolList{}, err
	} else if ok {
		observer = obs.ReportUsage
	}
	return toolList{compact.New(router, observer)}, nil
}

// execSourceFactory contributes the sandbox-backed exec tools.
type execSourceFactory struct{}

var _ resource.Factory[toolList] = execSourceFactory{}

func (execSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/exec",
		Deps: []resource.Requirement{depSandbox.Spec()},
	}
}

func (execSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	runner, err := depSandbox.Get(in)
	if err != nil {
		return nil, err
	}
	return execToolList(runner, goruntime.GOOS), nil
}

// execToolList builds the sandbox-backed exec tools for a target OS.
// exec_session is not offered on Windows: the Windows sandbox runs
// with OS-level write confinement, which the flowcraft backend does
// not combine with ConPTY TTY sessions yet (issue #38).
//
// Only exec_command takes part in escalation, and it does so through
// the runner's own front end (sandbox.EscalationProvider): a refusal is
// detected from a finished command's output, and exec_session has no
// finished result to inspect at Start, so a session is never prompted
// and never consults a remembered rule either — an interactive session
// runs under its session's mode.
func execToolList(
	runner sandbox.Runner,
	goos string,
) toolList {
	tools := toolList{exec.MustNewCommand(
		runner,
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

var _ resource.Factory[toolList] = applypatchSourceFactory{}

func (applypatchSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/applypatch",
		Deps: []resource.Requirement{depHostWorkspace.Spec()},
	}
}

func (applypatchSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := depHostWorkspace.Get(in)
	if err != nil {
		return nil, err
	}
	return toolList{applypatch.MustNew(ws)}, nil
}

// webfetchSourceFactory contributes the web_fetch tool.
type webfetchSourceFactory struct{}

var _ resource.Factory[toolList] = webfetchSourceFactory{}

func (webfetchSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/webfetch",
		Deps: []resource.Requirement{
			depOptNetPolicy.Spec(), depOptSessions.Spec(),
		},
	}
}

func (webfetchSourceFactory) New(ctx context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	t := webfetch.New()
	store, _, err := depOptSessions.TryGet(in)
	if err != nil {
		return toolList{}, err
	}
	policy, hasPolicy, err := depOptNetPolicy.TryGet(in)
	if err != nil {
		return toolList{}, err
	}
	if !hasPolicy {
		// No net policy resource in this deployment: fail closed.
		// There is no domain allowlist to enforce, but the SSRF
		// surface must not widen just because the resource is
		// missing — install the hardened client anyway (private,
		// loopback and link-local destinations refused, DNS pinned,
		// redirect hops revalidated). YOLO sessions keep their
		// private-destination bypass when the store is available.
		telemetry.Warn(ctx,
			"web_fetch: no net policy resource; serving with the default SSRF guard only")
		var allowPrivate func(context.Context) bool
		if store != nil {
			allowPrivate = func(ctx context.Context) bool {
				return ocsandbox.IsYOLO(ctx, store)
			}
		}
		t.SetSSRFGuard(allowPrivate)
		return toolList{t}, nil
	}
	gate := webfetch.DomainGate(policy.WebFetch)
	if store != nil {
		gate = webfetch.YOLOBypassGate(store, gate)
		// YOLO sessions disable the sandbox by definition: their
		// web_fetch should not be re-gated at the network layer, so
		// private destinations stay reachable for them too.
		t.SetAllowPrivate(func(ctx context.Context) bool {
			return policy.WebFetch.AllowPrivate ||
				ocsandbox.IsYOLO(ctx, store)
		})
	}
	t.SetGate(gate)
	return toolList{t}, nil
}

// websearchSourceFactory contributes the web_search tool. It needs no
// dependencies: providers and credentials come from the settings, and
// the keyless Parallel/Exa hosted MCP backends make the tool work with
// any model out of the box.
type websearchSourceFactory struct{}

var _ resource.Factory[toolList] = websearchSourceFactory{}

func (websearchSourceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "tool.Source", Impl: "opencraft/websearch"}
}

func (websearchSourceFactory) New(
	ctx context.Context,
	in resource.Input,
) (toolList, error) {
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

var _ resource.Factory[toolList] = askuserSourceFactory{}

func (askuserSourceFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "tool.Source", Impl: "opencraft/askuser"}
}

func (askuserSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	return toolList{askuser.New()}, nil
}

// filesSourceFactory contributes the workspace-backed file tools.
type filesSourceFactory struct{}

var _ resource.Factory[toolList] = filesSourceFactory{}

func (filesSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/files",
		Deps: []resource.Requirement{depHostWorkspace.Spec()},
	}
}

func (filesSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := depHostWorkspace.Get(in)
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

var _ resource.Factory[toolList] = viewimageSourceFactory{}

func (viewimageSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/viewimage",
		Deps: []resource.Requirement{depHostWorkspace.Spec()},
	}
}

func (viewimageSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	ws, err := depHostWorkspace.Get(in)
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

var _ resource.Factory[toolList] = imagegenSourceFactory{}

func (imagegenSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/imagegen",
		Deps: []resource.Requirement{
			depRouter.Spec(), depHostWorkspace.Spec(),
		},
	}
}

func (imagegenSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := depRouter.Get(in)
	if err != nil {
		return nil, err
	}
	ws, err := depHostWorkspace.Get(in)
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

var _ resource.Factory[toolList] = videogenSourceFactory{}

func (videogenSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/videogen",
		Deps: []resource.Requirement{
			depRouter.Spec(), depHostWorkspace.Spec(),
		},
	}
}

func (videogenSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	router, err := depRouter.Get(in)
	if err != nil {
		return nil, err
	}
	ws, err := depHostWorkspace.Get(in)
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

var _ resource.Factory[toolList] = permissionsSourceFactory{}

func (permissionsSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/permissions",
		Deps: []resource.Requirement{depExecPolicy.Spec()},
	}
}

func (permissionsSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	policy, err := depExecPolicy.Get(in)
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
		Deps: []resource.Requirement{depSessions.Spec()},
	}
}

func (planSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	store, err := depSessions.Get(in)
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
		Deps: []resource.Requirement{depSessions.Spec()},
	}
}

func (sessionsearchSourceFactory) New(
	_ context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	store, err := depSessions.Get(in)
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
		Deps: []resource.Requirement{depUserMemory.Spec()},
	}
}

func (rememberSourceFactory) New(
	ctx context.Context,
	in resource.Input,
) (any, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	binding, err := depUserMemory.Get(in)
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

var _ resource.Factory[toolList] = skillsSourceFactory{}

func (skillsSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/skills",
		Deps: []resource.Requirement{depSkills.Spec()},
	}
}

func (skillsSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	svc, err := depSkills.Get(in)
	if err != nil {
		return nil, err
	}
	return toolList(skillstools.MustNew(svc).Tools()), nil
}

// agentlifecycleSourceFactory contributes the create_agent /
// unregister_agent tools over the persistent subagent registry.
type agentlifecycleSourceFactory struct{}

var _ resource.Factory[toolList] = agentlifecycleSourceFactory{}

func (agentlifecycleSourceFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "tool.Source",
		Impl: "opencraft/agentlifecycle",
		Deps: []resource.Requirement{depAgentLife.Spec()},
	}
}

func (agentlifecycleSourceFactory) New(_ context.Context, in resource.Input) (toolList, error) {
	if !sourceEnabled(in) {
		return toolList{}, nil
	}
	lifecycle, err := depAgentLife.Get(in)
	if err != nil {
		return nil, err
	}
	return toolList(agents.MustNew(lifecycle).Tools()), nil
}
