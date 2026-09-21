package host

import (
	"context"
	"testing"
)

// TestAssemblyReasonRoundTrip pins the context plumbing the assembly
// attribution log depends on: a tagged caller reports its own reason, an
// untagged or nil context reports "unknown" instead of an empty string,
// and an empty tag is a no-op rather than a blank log field.
func TestAssemblyReasonRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		ctx  context.Context
		want AssemblyReason
	}{
		{name: "nil", ctx: nil, want: ReasonUnknown},
		{name: "untagged", ctx: context.Background(), want: ReasonUnknown},
		{
			name: "tagged",
			ctx: WithAssemblyReason(context.Background(),
				ReasonPluginChange),
			want: ReasonPluginChange,
		},
		{
			name: "empty tag is ignored",
			ctx:  WithAssemblyReason(context.Background(), ""),
			want: ReasonUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AssemblyReasonFrom(tc.ctx); got != tc.want {
				t.Fatalf("AssemblyReasonFrom = %q, want %q", got, tc.want)
			}
		})
	}
}
