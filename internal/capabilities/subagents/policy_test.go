package subagents

import (
	"context"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/resource"
)

// TestPolicyAllowsAndBlocks pins the matcher's resolution order:
// blocking wins, and a non-empty allowlist is exhaustive.
func TestPolicyAllowsAndBlocks(t *testing.T) {
	open := NewPolicy(nil, nil)
	if !open.Empty() || !open.Allows("anything") {
		t.Fatalf("empty policy restricts: %v", open.Describe())
	}
	// The nil policy is the deployment without the resource.
	var nilPolicy *Policy
	if !nilPolicy.Empty() || !nilPolicy.Allows("researcher") {
		t.Fatal("nil policy restricts")
	}
	if nilPolicy.Allows("  ") {
		t.Fatal("nil policy allows a blank target")
	}

	allow := NewPolicy([]string{"researcher", "writer*"}, nil)
	for name, want := range map[string]bool{
		"researcher": true,
		"writer":     true,
		"writer-two": true,
		"reviewer":   false,
		"":           false,
		"  ":         false,
	} {
		if got := allow.Allows(name); got != want {
			t.Errorf("allowlist Allows(%q) = %v, want %v", name, got, want)
		}
	}

	blocked := NewPolicy(nil, []string{"danger*"})
	if blocked.Allows("dangerous") || !blocked.Allows("researcher") {
		t.Fatalf("blocklist does not restrict: %v", blocked.Describe())
	}
	// A target named by both lists resolves to blocked, never to
	// allowed: the settings validator rejects the pair, and the runtime
	// still has to be safe if a hand-written document slips through.
	both := NewPolicy([]string{"researcher"}, []string{"researcher"})
	if both.Allows("researcher") {
		t.Fatal("a target on both lists was allowed")
	}
}

// TestPolicyReportsWhatItAccepted pins the read side the settings page
// uses, including that it hands out copies.
func TestPolicyReportsWhatItAccepted(t *testing.T) {
	policy := NewPolicy([]string{" a ", "a", ""}, []string{"b"})
	if got := policy.Allowed(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("Allowed() = %v, want the trimmed de-duped list", got)
	}
	got := policy.Allowed()
	got[0] = "mutated"
	if again := policy.Allowed(); again[0] != "a" {
		t.Fatal("Allowed() handed out the policy's own slice")
	}
	described := policy.Describe()
	if !strings.Contains(described, "allowed targets: a") ||
		!strings.Contains(described, "blocked targets: b") {
		t.Fatalf("Describe() = %q", described)
	}
	if (&Policy{}).Describe() == "" {
		t.Fatal("an empty policy describes nothing, want an explanation")
	}
}

// TestPolicyFactoryValidation covers the deploy seam: a malformed
// pattern or a self-contradicting pair fails the assembly instead of
// producing a policy that does not do what it says.
func TestPolicyFactoryValidation(t *testing.T) {
	factory := PolicyFactory{}
	built, err := factory.New(context.Background(), resource.Input{
		Settings: []byte(`{"allowed_targets":["researcher"],"blocked_targets":[]}`),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	policy, ok := built.(*Policy)
	if !ok || !policy.Allows("researcher") || policy.Allows("reviewer") {
		t.Fatalf("built = %#v", built)
	}

	for name, settings := range map[string]string{
		"malformed pattern": `{"allowed_targets":["researcher["]}`,
		"both lists":        `{"allowed_targets":["a"],"blocked_targets":["a"]}`,
		"separator":         `{"allowed_targets":["a/b"]}`,
		"undecodable":       `{`,
	} {
		if _, err := factory.New(context.Background(), resource.Input{
			Settings: []byte(settings),
		}); err == nil {
			t.Errorf("%s: accepted %s", name, settings)
		}
	}
	// The resource kind is what the deploy document names; the resource
	// is optional, so a document without it must still assemble.
	if factory.Spec().Kind != PolicyResourceKind {
		t.Fatalf("kind = %q", factory.Spec().Kind)
	}
}
