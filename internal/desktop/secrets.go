package desktop

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/GizClaw/opencraft/internal/config"
	"github.com/GizClaw/opencraft/internal/secrets"
)

// reconcileInferenceKeys keeps the inference credential wiring
// consistent with the OS credential store:
//
//   - literal keys are moved into the store and the config rewritten
//     to a ${secret:keychain.<account>} reference;
//   - references whose store entry no longer exists (deleted items,
//     a store reset, or a reference written by an earlier build) are
//     reverted to an empty literal so the settings page asks for the
//     key again instead of failing turns with "secret not found".
//
// Reconciliation is best-effort and never blocks startup: store
// failures leave the config in place.
func (a *App) reconcileInferenceKeys() {
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
		switch {
		case in.KeySource == config.KeyLiteral && strings.TrimSpace(in.KeyValue) != "":
			account := secrets.AccountFor(in.DeploymentID(i + 1))
			if err := a.secrets.Set(ctx, account, in.KeyValue); err != nil {
				fmt.Fprintf(os.Stderr,
					"opencraft: key migration for %q failed, keeping literal key: %v\n",
					in.DeploymentID(i+1), err)
				continue
			}
			// Read back before dropping the literal so a failed write
			// can never strand the credential.
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
		case in.KeySource == config.KeyKeychain && strings.TrimSpace(in.KeyValue) != "":
			account := in.KeyValue
			_, found, err := a.secrets.Get(ctx, account)
			switch {
			case err != nil:
				fmt.Fprintf(os.Stderr,
					"opencraft: credential store lookup for %q failed: %v\n",
					account, err)
			case !found:
				fmt.Fprintf(os.Stderr,
					"opencraft: credential entry for %q is missing; please re-enter the key in Settings\n",
					account)
				// Clear the key so the settings page flags the row and
				// the router stops failing with "secret not found".
				in.KeySource = config.KeyLiteral
				in.KeyValue = ""
				changed = true
			}
		}
	}
	if !changed {
		return
	}
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		fmt.Fprintf(os.Stderr,
			"opencraft: key migration config write failed: %v\n", err)
	}
}
