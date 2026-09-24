package sessions

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

func FuzzRequireID(f *testing.F) {
	for _, seed := range []string{
		"s-abc",
		"s-../../etc/passwd",
		"",
		"x",
		"../escape",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, id string) {
		_ = ids.IsSession(id)
		_ = requireID(id)
	})
}
