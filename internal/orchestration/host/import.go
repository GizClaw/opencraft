package host

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
)

// ImportSession writes one neutral session bundle into the Host's
// store, from which a later turn continues: the transcript the import
// writes is what the model's window is projected from, so there is no
// second history to seed. Store.Import dedupes by Source; a source that
// is already imported returns the existing id and only backfills the
// accounting. Fresh imports also get the same asynchronous LLM
// display-title generation as normal turns; the bundle title remains
// the instant fallback when inference is not configured or generation
// fails.
func (h *Host) ImportSession(
	ctx context.Context, req ocsessions.ImportRequest,
) (string, error) {
	if h == nil || h.store == nil {
		return "", ErrSessionStoreNotReady
	}

	h.importMu.Lock()
	defer h.importMu.Unlock()

	source := strings.TrimSpace(req.Source)
	// ImportedBySources answers with the *ready* conversation for each
	// source, which is exactly the "this bundle is already here" case:
	// the import below is a no-op that returns the same id, so the only
	// work left is accounting it once.
	existing, err := h.store.ImportedBySources(ctx, []string{source})
	if err != nil {
		return "", fmt.Errorf("host: import dedupe lookup: %w", err)
	}
	id, err := h.store.Import(ctx, req)
	if err != nil {
		return "", err
	}
	// Account the source-reported token totals exactly once. An import
	// arrives as a pre-aggregated cumulative total, so it is written
	// only when the session has no recorded usage yet: fresh imports
	// record right after the transcript lands, and an already-imported
	// bundle gets a backfill when no usage was captured.
	if _, ready := existing[source]; ready {
		h.recordImportUsage(ctx, id, req.Usage, importUsageAt(req))
		return id, nil
	}
	h.recordImportUsage(ctx, id, req.Usage, importUsageAt(req))
	h.launchAutoTitle(context.WithoutCancel(ctx), id)
	return id, nil
}

// recordImportUsage persists an imported session's source-recorded
// totals through the store's atomic empty-seed write and forwards them
// to the user-level recorder installed on the Host's manager. at is
// the earliest turn time when the bundle carries turns, so historical
// imports land in their own hourly buckets instead of being attributed
// to the import moment.
func (h *Host) recordImportUsage(
	ctx context.Context,
	id string,
	usage *ocsessions.Usage,
	at time.Time,
) {
	if h == nil || h.store == nil || usage == nil || usage.TotalTokens <= 0 {
		return
	}
	recorded, err := h.store.RecordUsageIfEmpty(ctx, id, *usage)
	if err != nil {
		telemetry.WarnErr(ctx, "host: record imported session usage failed", err,
			otellog.String("conversation.id", id))
		return
	}
	if recorded {
		h.forwardUsageRecorder(ctx, id, *usage, at)
	}
}

// importUsageAt picks the timestamp that best represents when an
// imported bundle's tokens were consumed: the earliest turn time in
// the bundle. Bundles without turn timestamps fall back to the import
// moment.
func importUsageAt(req ocsessions.ImportRequest) time.Time {
	var earliest time.Time
	for _, turn := range req.Turns {
		if turn.At.IsZero() {
			continue
		}
		if earliest.IsZero() || turn.At.Before(earliest) {
			earliest = turn.At
		}
	}
	if earliest.IsZero() {
		return time.Now().UTC()
	}
	return earliest.UTC()
}

// forwardUsageRecorder sends one usage delta to the user-level recorder
// installed on the Host's manager.
func (h *Host) forwardUsageRecorder(
	ctx context.Context,
	contextID string,
	usage ocsessions.Usage,
	at time.Time,
) {
	if h == nil || h.usageRecorder == nil {
		return
	}
	telemetry.WarnErr(ctx, "host: record user-level usage failed",
		h.usageRecorder(ctx, h.workspaceID, contextID, usage, at))
}
