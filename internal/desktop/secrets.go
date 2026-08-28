package desktop

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/secrets"
)

// migrateInferenceKeys moves literal provider keys from opencraft.yaml
// into the OS credential store, rewriting each instance to a
// ${secret:keychain.<account>} reference. Migration is best-effort:
// store failures leave the literal in place, and a broken config never
// blocks startup.
func (a *App) migrateInferenceKeys() {
	if a.secrets == nil || !a.secrets.Available() {
		return
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cfg, err := config.LoadInference(a.userDir)
	if err != nil {
		return
	}
	changed := false
	for i := range cfg.Instances {
		in := &cfg.Instances[i]
		if in.KeySource != config.KeyLiteral || strings.TrimSpace(in.KeyValue) == "" {
			continue
		}
		account := secrets.AccountFor(in.DeploymentID(i + 1))
		if err := a.secrets.Set(ctx, account, in.KeyValue); err != nil {
			fmt.Fprintf(os.Stderr,
				"opencraft: key migration for %q failed, keeping literal key: %v\n",
				in.DeploymentID(i+1), err)
			continue
		}
		// Read back before dropping the literal so a failed write can
		// never strand the credential.
		got, found, err := a.secrets.Get(ctx, account)
		if err != nil || !found || got != in.KeyValue {
			fmt.Fprintf(os.Stderr,
				"opencraft: key migration verification for %q failed, keeping literal key\n",
				in.DeploymentID(i+1))
			continue
		}
		in.KeySource = config.KeyKeychain
		in.KeyValue = account
		changed = true
	}
	if !changed {
		return
	}
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr,
			"opencraft: key migration config write failed: %v\n", err)
	}
}
