package app

import (
	"context"
	"encoding/json"
	goruntime "runtime"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/sandbox"
	"github.com/GizClaw/flowcraft/core/sandbox/bwrap"
	sandboxlocal "github.com/GizClaw/flowcraft/core/sandbox/local"
	"github.com/GizClaw/flowcraft/core/sandbox/seatbelt"

	"github.com/GizClaw/opencraft/internal/execd"
)

// sandboxFactory implements the sandbox.Runner resource with the
// platform backend (seatbelt on macOS, bwrap on Linux, local
// elsewhere), env-expanded settings, and the execpolicy resource's
// approval wrapper.
type sandboxFactory struct{}

var _ resource.Factory = sandboxFactory{}

func (sandboxFactory) Spec() resource.Spec {
	return resource.Spec{
		Kind: "sandbox.Runner",
		Impl: "opencraft",
		Deps: []resource.DepSpec{
			{Name: "execpolicy", Type: "opencraft.execpolicy", Required: true},
		},
	}
}

type sandboxSettings struct {
	Root            string   `json:"root"`
	WritablePaths   []string `json:"writable_paths,omitempty"`
	Remote          bool     `json:"remote,omitempty"`
	EnvPolicy       *struct {
		Allow  []string          `json:"allow,omitempty"`
		Inject map[string]string `json:"inject,omitempty"`
	} `json:"env_policy,omitempty"`
}

// envPolicy converts the configured environment policy into the
// sandbox EnvPolicy applied to every spawn. A nil env_policy yields a
// zero policy (inherit host env, no injection).
func (s sandboxSettings) envPolicy() sandbox.EnvPolicy {
	if s.EnvPolicy == nil {
		return sandbox.EnvPolicy{}
	}
	return sandbox.EnvPolicy{
		Allow:  s.EnvPolicy.Allow,
		Inject: s.EnvPolicy.Inject,
	}
}

// sandboxPolicy converts decoded settings into the policy handed to the
// execd child. A nil env_policy leaves the policy's EnvPolicy nil, so
// the child applies an empty environment policy (inherit host env,
// no injection).
func (s sandboxSettings) sandboxPolicy() SandboxPolicy {
	pol := SandboxPolicy{WritablePaths: s.WritablePaths}
	if s.EnvPolicy != nil {
		pol.EnvPolicy = &EnvPolicyConfig{
			Allow:  s.EnvPolicy.Allow,
			Inject: s.EnvPolicy.Inject,
		}
	}
	return pol
}

func (f sandboxFactory) New(ctx context.Context, in resource.Input) (any, error) {
	s, err := resource.DecodeTyped[sandboxSettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft sandbox: decode settings: %v", err)
	}
	if s.Root == "" {
		return nil, errdefs.Validationf(
			"opencraft sandbox: settings.root is required")
	}
	policy, ok := in.Dep("execpolicy")
	if !ok {
		return nil, errdefs.Validationf(
			"opencraft sandbox: execpolicy dependency is required")
	}
	execPolicy, ok := policy.(execPolicy)
	if !ok {
		return nil, errdefs.Validationf(
			"opencraft sandbox: execpolicy dep is %T, want execPolicy",
			policy)
	}
	var runner sandbox.Runner
	if s.Remote {
		polJSON, err := json.Marshal(s.sandboxPolicy())
		if err != nil {
			return nil, errdefs.Validationf(
				"opencraft sandbox: encode env policy: %v", err)
		}
		client, _, stop, err := execd.Launch(ctx, s.Root, string(polJSON))
		if err != nil {
			return nil, err
		}
		runner, err = execd.NewRemoteRunner(client, stop)
		if err != nil {
			return nil, err
		}
	} else {
		switch goruntime.GOOS {
		case "darwin":
			runner, err = seatbelt.New(s.Root,
				seatbelt.WithWritablePaths(s.WritablePaths...))
		case "linux":
			runner, err = bwrap.New(s.Root,
				bwrap.WithWritablePaths(s.WritablePaths...))
		default:
			runner = sandboxlocal.New(s.Root)
		}
		if err != nil {
			return nil, err
		}
	}
	// Env policy is part of the sandbox contract in both modes: the
	// execd child applies it as its DefaultEnv, and the local backend
	// gets it through WithDefaults so remote:false behaves identically
	// instead of silently inheriting the host environment.
	runner = sandbox.WithDefaults(runner, sandbox.ExecOptions{
		Env: s.envPolicy(),
	})
	return sandbox.WithApproval(
		runner, execPolicy.Approve, execPolicy.Allowlist()), nil
}

var _ sandbox.Runner = (*sandboxlocal.Runner)(nil)
