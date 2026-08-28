package desktop

import (
	"context"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

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
	migrated := 0
	for i := range cfg.Instances {
		in := &cfg.Instances[i]
		switch {
		case in.KeySource == config.KeyLiteral && strings.TrimSpace(in.KeyValue) != "":
			account := secrets.AccountFor(in.DeploymentID(i + 1))
			if err := a.secrets.Set(ctx, account, in.KeyValue); err != nil {
				telemetry.Error(ctx, "opencraft: key migration failed, keeping literal key",
					otellog.String("deployment", in.DeploymentID(i+1)),
					otellog.String("error", err.Error()))
				continue
			}
			// Read back before dropping the literal so a failed write
			// can never strand the credential.
			got, found, err := a.secrets.Get(ctx, account)
			if err != nil || !found || got != in.KeyValue {
				telemetry.Error(ctx, "opencraft: key migration verification failed, keeping literal key",
					otellog.String("deployment", in.DeploymentID(i+1)),
					otellog.String("error", errText(err)))
				continue
			}
			in.KeySource = config.KeyKeychain
			in.KeyValue = account
			changed = true
			migrated++
		case in.KeySource == config.KeyKeychain && strings.TrimSpace(in.KeyValue) != "":
			account := in.KeyValue
			_, found, err := a.secrets.Get(ctx, account)
			switch {
			case err != nil:
				telemetry.Error(ctx, "opencraft: credential store lookup failed",
					otellog.String("account", account),
					otellog.String("error", err.Error()))
			case !found:
				telemetry.Warn(ctx, "opencraft: credential entry missing; please re-enter the key in Settings",
					otellog.String("account", account))
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
	if migrated > 0 {
		telemetry.Info(ctx, "opencraft: migrated inference keys to credential store",
			otellog.Int("count", migrated))
	}
	if err := config.WriteInference(a.userDir, cfg); err != nil {
		telemetry.Error(ctx, "opencraft: key migration config write failed",
			otellog.String("error", err.Error()))
	}
}

// errText renders a possibly-nil error for telemetry attributes.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
