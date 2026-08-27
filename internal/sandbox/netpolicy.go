package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/resource"
	corenet "github.com/GizClaw/flowcraft/core/utils/net"
)

// NetPolicyResourceKind is the deploy resource kind for the assembled
// network policy consumed by the sandbox runner defaults and the
// web_fetch domain gate.
const NetPolicyResourceKind = "opencraft.netpolicy"

// NetPolicyImpl is the deploy impl id.
const NetPolicyImpl = "local"

// Policy is the assembled network policy.
type Policy struct {
	Exec     corenet.NetPolicy
	WebFetch WebFetchPolicy
}

// WebFetchPolicy is the domain gate applied by web_fetch on top of the
// exec posture: allow/deny host lists plus an SSRF guard that blocks
// private/loopback/link-local destinations unless AllowPrivate is set.
type WebFetchPolicy struct {
	AllowHosts   []string
	DenyHosts    []string
	AllowPrivate bool
}

// NetPolicySettings is the deploy-document settings shape.
type NetPolicySettings struct {
	Exec     *ExecNetSettings  `json:"exec,omitempty"`
	WebFetch *WebFetchSettings `json:"web_fetch,omitempty"`
}

// ExecNetSettings configures the sandbox exec network posture.
type ExecNetSettings struct {
	Mode       string            `json:"mode,omitempty"` // default | deny-all | allow-list | proxy
	AllowHosts []string          `json:"allow_hosts,omitempty"`
	Proxy      string            `json:"proxy,omitempty"`
	Rules      []NetRuleSettings `json:"rules,omitempty"`
}

// NetRuleSettings is one allow/deny host rule.
type NetRuleSettings struct {
	Action string `json:"action"` // allow | deny
	Host   string `json:"host"`
	Port   int    `json:"port,omitempty"`
}

// WebFetchSettings configures the web_fetch domain gate.
type WebFetchSettings struct {
	AllowHosts   []string `json:"allow_hosts,omitempty"`
	DenyHosts    []string `json:"deny_hosts,omitempty"`
	AllowPrivate bool     `json:"allow_private,omitempty"`
}

// NetPolicyFactory builds the opencraft.netpolicy resource.
type NetPolicyFactory struct{}

var _ resource.Factory = NetPolicyFactory{}

// Spec implements resource.Factory.
func (NetPolicyFactory) Spec() resource.Spec {
	return resource.Spec{Kind: NetPolicyResourceKind, Impl: NetPolicyImpl}
}

// New implements resource.Factory. Absent sections keep safe defaults:
// exec stays NetDefault (host networking, the historical behavior) and
// web_fetch keeps the SSRF guard enabled.
func (NetPolicyFactory) New(_ context.Context, in resource.Input) (any, error) {
	settings, err := resource.DecodeTyped[NetPolicySettings](
		in.Settings, resource.ExpandEnv())
	if err != nil {
		return nil, errdefs.Validationf(
			"opencraft netpolicy: decode settings: %v", err)
	}
	pol := Policy{
		Exec:     corenet.NetPolicy{Mode: corenet.NetDefault},
		WebFetch: WebFetchPolicy{},
	}
	if settings.Exec != nil {
		pol.Exec, err = buildExecNet(*settings.Exec)
		if err != nil {
			return nil, err
		}
	}
	if settings.WebFetch != nil {
		pol.WebFetch = WebFetchPolicy{
			AllowHosts:   settings.WebFetch.AllowHosts,
			DenyHosts:    settings.WebFetch.DenyHosts,
			AllowPrivate: settings.WebFetch.AllowPrivate,
		}
	}
	return pol, nil
}

func buildExecNet(s ExecNetSettings) (corenet.NetPolicy, error) {
	var mode corenet.NetMode
	switch strings.ToLower(strings.TrimSpace(s.Mode)) {
	case "", "default":
		mode = corenet.NetDefault
	case "deny-all", "deny_all":
		mode = corenet.NetDenyAll
	case "allow-list", "allow_list":
		mode = corenet.NetAllowList
	case "proxy":
		mode = corenet.NetProxy
	default:
		return corenet.NetPolicy{}, errdefs.Validationf(
			"opencraft netpolicy: unknown exec.mode %q (default|deny-all|allow-list|proxy)",
			s.Mode)
	}
	pol := corenet.NetPolicy{
		Mode:       mode,
		AllowHosts: s.AllowHosts,
		Proxy:      s.Proxy,
	}
	for i, r := range s.Rules {
		var action corenet.NetAction
		switch strings.ToLower(strings.TrimSpace(r.Action)) {
		case "allow":
			action = corenet.NetAllow
		case "deny":
			action = corenet.NetDeny
		default:
			return corenet.NetPolicy{}, errdefs.Validationf(
				"opencraft netpolicy: rules[%d].action must be allow|deny", i)
		}
		if strings.TrimSpace(r.Host) == "" {
			return corenet.NetPolicy{}, errdefs.Validationf(
				"opencraft netpolicy: rules[%d].host is required", i)
		}
		pol.Rules = append(pol.Rules, corenet.NetRule{
			Action: action,
			Host:   r.Host,
			Port:   r.Port,
		})
	}
	if err := pol.Validate(); err != nil {
		return corenet.NetPolicy{}, fmt.Errorf(
			"opencraft netpolicy: exec: %w", err)
	}
	return pol, nil
}

// Register adds the netpolicy factory to r.
func registerNetPolicy(r *resource.Registry) error {
	return r.Register(NetPolicyFactory{})
}
