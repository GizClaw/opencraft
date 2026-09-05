//go:build yoloonly

package bindings

import (
	"testing"

	"github.com/GizClaw/opencraft/internal/adapters/desktopv2/core"
)

func TestConfigProfileYoloOnlyBuild(t *testing.T) {
	b := NewConfig(core.NewCore(t.TempDir(), t.TempDir(), ""))
	if got := b.Profile(); !got.YoloOnly {
		t.Fatal("yoloonly build did not report the profile")
	}
}
