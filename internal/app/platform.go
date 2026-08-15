package app

import (
	"context"
	"encoding/json"
	"path/filepath"
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
// elsewhere) and env-expanded settings. holder receives the built
// execpolicy manager so the runtime can expose it to tools through
// the turn host.
type sandboxFactory struct {
	holder *policyHolder
}

var _ resource.Factory = sandboxFactory{}

func (sandboxFactory) Spec() resource.Spec {
	return resource.Spec{Kind: "sandbox.Runner", Impl: "opencraft"}
}

type sandboxSettings struct {
	Root            string   `json:"root"`
	WritablePaths   []string `json:"writable_paths,omitempty"`
	Remote          bool     `json:"remote,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
	EnvPolicy       *struct {
		Allow  []string          `json:"allow,omitempty"`
		Inject map[string]string `json:"inject,omitempty"`
	} `json:"env_policy,omitempty"`
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
	policy, err := New(
		s.AllowedCommands,
		filepath.Join(s.Root, ".opencraft", "approvals.yaml"),
	)
	if err != nil {
		return nil, err
	}
	if f.holder != nil {
		f.holder.set(policy)
	}
	var runner sandbox.Runner
	if s.Remote {
		polJSON, err := json.Marshal(s.sandboxPolicy())
		if err != nil {
			return nil, errdefs.Validationf(
				"opencraft sandbox: encode env policy: %v", err)
		}
		client, stop, err := execd.Launch(ctx, s.Root, string(polJSON))
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
	return sandbox.WithApproval(runner, policy.Approve, policy.Allowlist()), nil
}

var _ sandbox.Runner = (*sandboxlocal.Runner)(nil)
