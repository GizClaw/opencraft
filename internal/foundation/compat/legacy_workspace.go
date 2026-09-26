package compat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/telemetry"
	otellog "go.opentelemetry.io/otel/log"

	"github.com/GizClaw/opencraft/internal/foundation/ids"
)

const (
	legacyMigratingKey = "legacy_migrating"
	legacyMigratedKey  = "legacy_migrated"
)

// legacyWorkspaceMeta mirrors the pre-SQLite <session>/meta.json.
type legacyWorkspaceMeta struct {
	legacyWorkspaceUsage
	TurnCount    int       `json:"turn_count,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	ImportSource string    `json:"import_source,omitempty"`
	ImportReady  bool      `json:"import_ready,omitempty"`
}

// legacyWorkspaceUsage mirrors the session usage document of the old
// per-session JSON store.
type legacyWorkspaceUsage struct {
	Model            string `json:"model,omitempty"`
	InputTokens      int64  `json:"input_tokens,omitempty"`
	OutputTokens     int64  `json:"output_tokens,omitempty"`
	TotalTokens      int64  `json:"total_tokens,omitempty"`
	CacheReadTokens  int64  `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64  `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	LatencyMs        int64  `json:"latency_ms,omitempty"`
}

// legacyWorkspaceArtifact mirrors the old turn artifact list.
type legacyWorkspaceArtifact struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes,omitempty"`
}

// legacyWorkspaceTurn is one pre-SQLite history/000001.json document.
type legacyWorkspaceTurn struct {
	Seq         int                       `json:"seq"`
	At          time.Time                 `json:"at"`
	RequestedAt time.Time                 `json:"requested_at,omitzero"`
	StartedAt   time.Time                 `json:"started_at,omitzero"`
	FinishedAt  time.Time                 `json:"finished_at,omitzero"`
	RunID       string                    `json:"run_id,omitempty"`
	Messages    []message.Message         `json:"messages"`
	Artifacts   []legacyWorkspaceArtifact `json:"artifacts,omitempty"`
}

// WorkspaceData imports pre-SQLite JSON transcripts and per-session
// state documents into an already-migrated workspace database, writing
// through importer (the store that owns those rows). It carries no
// schema_migrations row: the data it moves lives in the filesystem, so
// each conversation records its own progress in conversation_state
// instead, and interrupted runs resume without duplicates.
func WorkspaceData(
	ctx context.Context, root string, importer WorkspaceImporter,
) error {
	if importer == nil {
		return fmt.Errorf("compat: workspace importer is required")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("compat: read legacy sessions %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !ids.IsSession(entry.Name()) {
			continue
		}
		id := entry.Name()
		if err := importLegacyConversation(ctx, importer, root, id); err != nil {
			return fmt.Errorf("compat: legacy session %s: %w", id, err)
		}
	}
	return nil
}

// importLegacyConversation reads one legacy session and hands it to the
// importer. Reading and deciding live here; writing lives with the store.
func importLegacyConversation(
	ctx context.Context, importer WorkspaceImporter, root, id string,
) error {
	dir := filepath.Join(root, id)
	historyDir := filepath.Join(dir, "history")
	if _, err := os.Stat(historyDir); err != nil {
		if os.IsNotExist(err) {
			return markLegacyMigrated(ctx, importer, id)
		}
		return err
	}
	if _, found, err := importer.State(
		ctx, id, legacyMigratedKey,
	); err != nil {
		return err
	} else if found {
		return nil
	}
	if err := importer.SetState(
		ctx, id, legacyMigratingKey, []byte("1"),
	); err != nil {
		if errors.Is(err, ErrConversationRetired) {
			// The user deleted this conversation after it was
			// adopted, and the store refuses documents for it. The
			// marker only exists to resume an interrupted import, and
			// there is nothing to import into a deleted conversation.
			telemetry.Info(ctx,
				"compat: legacy session skipped; its conversation was deleted",
				otellog.String("session", id))
			return nil
		}
		return err
	}

	meta, err := readLegacyMeta(dir)
	if err != nil {
		return err
	}
	usageJSON, err := json.Marshal(meta.legacyWorkspaceUsage)
	if err != nil {
		return fmt.Errorf("compat: marshal legacy usage: %w", err)
	}
	createdAt := meta.CreatedAt
	updatedAt := meta.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	files, err := filepath.Glob(filepath.Join(historyDir, "*.json"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	payload := WorkspaceImport{
		ID:           id,
		Title:        meta.Title,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		TurnCount:    meta.TurnCount,
		MessageCount: meta.MessageCount,
		UsageJSON:    usageJSON,
		ImportSource: meta.ImportSource,
		ImportReady:  meta.ImportReady,
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var turn legacyWorkspaceTurn
		if err := json.Unmarshal(data, &turn); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		msgs := filterLegacyArchive(turn.Messages)
		if len(msgs) == 0 {
			continue
		}
		runID := turn.RunID
		if runID == "" {
			runID = fmt.Sprintf("legacy-%s-%06d", id, turn.Seq)
		}
		at := turn.At
		if at.IsZero() {
			at = createdAt
		}
		requested := turn.RequestedAt
		started := turn.StartedAt
		finished := turn.FinishedAt
		if requested.IsZero() {
			requested = at
		}
		if started.IsZero() {
			started = at
		}
		if finished.IsZero() {
			finished = at
		}
		artifacts, err := json.Marshal(turn.Artifacts)
		if err != nil {
			return fmt.Errorf("compat: marshal legacy artifacts: %w", err)
		}
		imported := WorkspaceImportTurn{
			RunID:         runID,
			At:            at,
			RequestedAt:   requested,
			StartedAt:     started,
			FinishedAt:    finished,
			ArtifactsJSON: artifacts,
			Messages:      make([]WorkspaceImportMessage, 0, len(msgs)),
		}
		for _, m := range msgs {
			imported.Messages = append(imported.Messages, WorkspaceImportMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}
		payload.Turns = append(payload.Turns, imported)
	}
	docs, err := readLegacyStateDocs(dir)
	if err != nil {
		return err
	}
	payload.State = docs
	if err := importer.ImportConversation(ctx, payload); err != nil {
		if errors.Is(err, ErrConversationRetired) {
			// The delete landed between the marker above and this
			// write: the same skip, one race later.
			telemetry.Info(ctx,
				"compat: legacy session skipped; its conversation was deleted",
				otellog.String("session", id))
			return nil
		}
		return err
	}
	if err := markLegacyMigrated(ctx, importer, id); err != nil {
		return err
	}
	telemetry.WarnErr(ctx, "compat: remove legacy history failed",
		os.RemoveAll(historyDir))
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			telemetry.WarnErr(ctx, "compat: remove legacy session file failed",
				os.Remove(filepath.Join(dir, entry.Name())),
				otellog.String("session", id))
		}
	}
	return nil
}

func markLegacyMigrated(
	ctx context.Context, importer WorkspaceImporter, id string,
) error {
	err := importer.SetState(ctx, id, legacyMigratedKey, []byte("1"))
	if errors.Is(err, ErrConversationRetired) {
		// The conversation was deleted since this directory was last
		// read; there is nothing left to mark as migrated.
		return nil
	}
	return err
}

func readLegacyMeta(dir string) (legacyWorkspaceMeta, error) {
	var meta legacyWorkspaceMeta
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return meta, nil
		}
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, fmt.Errorf("decode meta.json: %w", err)
	}
	return meta, nil
}

// readLegacyStateDocs reads the per-session state documents (plan,
// approvals, ...) that lived next to the transcript. meta.json and
// runs.json are metadata, not state.
func readLegacyStateDocs(dir string) (map[string][]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		switch name {
		case "meta", "runs":
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

// filterLegacyArchive keeps the parts the archive understands.
func filterLegacyArchive(msgs []message.Message) []message.Message {
	var archived []message.Message
	for _, m := range msgs {
		var parts []message.Part
		for _, p := range m.Content.Parts {
			switch part := p.(type) {
			case message.TextPart:
				parts = append(parts, part)
			case message.ReasoningPart:
				parts = append(parts, part)
			case message.ToolCallPart:
				parts = append(parts, part)
			case message.ToolResultPart:
				parts = append(parts, part)
			case message.ImagePart:
				parts = append(parts, part)
			case message.AudioPart:
				parts = append(parts, part)
			case message.VideoPart:
				parts = append(parts, part)
			case message.FilePart:
				parts = append(parts, part)
			case message.DataPart:
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			archived = append(archived, message.Message{
				Role:    m.Role,
				Content: message.Content{Parts: parts},
			})
		}
	}
	return archived
}
