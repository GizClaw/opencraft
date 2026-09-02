package app

import (
	"testing"

	"github.com/GizClaw/flowcraft/core/sandbox"
)

func FuzzExecPolicyRule(f *testing.F) {
	for _, seed := range []string{
		"ls",
		"git *",
		"/bin/sh -c *",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, rule string) {
		// Rule construction must never panic; invalid rules return an
		// error and are skipped.
		a, err := sandbox.NewAllowlist(rule)
		if err != nil {
			return
		}
		_ = a.Rules()
	})
}
