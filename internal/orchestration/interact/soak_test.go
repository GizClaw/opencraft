//go:build soak

package interact

import (
	"context"
	"testing"

	"github.com/GizClaw/flowcraft/core/event"
)

type fakeSoakRuntime struct {
	attached int
}

func (f *fakeSoakRuntime) Attach(
	_ context.Context,
	_ event.Pattern,
	_ event.Sink,
	_ ...event.AttachOption,
) (func(), error) {
	f.attached++
	return func() { f.attached-- }, nil
}

// TestSoakBrokerAttachDetach asserts Broker.Attach/Close never leaks
// subscriptions across many cycles.
func TestSoakBrokerAttachDetach(t *testing.T) {
	rt := &fakeSoakRuntime{}
	for i := 0; i < 100; i++ {
		b := New(rt, Auto{})
		if err := b.Attach(context.Background()); err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
		b.Close()
	}
	if rt.attached != 0 {
		t.Fatalf("%d subscriptions still attached", rt.attached)
	}
}
