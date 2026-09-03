package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

const (
	legacyMigratingKey = "legacy_migrating"
	legacyMigratedKey  = "legacy_migrated"
)

// legacyMeta mirrors the pre-SQLite <session>/meta.json document.
type legacyMeta struct {
	Usage
	TurnCount    int       `json:"turn_count,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitempty"`
	ImportSource string    `json:"import_source,omitempty"`
	ImportReady  bool      `json:"import_ready,omitempty"`
}

// migrateLegacy imports pre-SQLite JSON transcripts and state documents
// into archive tables once. Imported turns use idempotent synthetic
// run ids so an interrupted migration resumes without duplicates.
func (s *Store) migrateLegacy(ctx context.Context) error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "s-") {
			continue
		}
		id := entry.Name()
		if err := s.migrateLegacyConversation(ctx, id); err != nil {
			return fmt.Errorf("sessions: migrate %s: %w", id, err)
		}
	}
	return nil
}

func (s *Store) migrateLegacyConversation(ctx context.Context, id string) error {
	dir := s.dir(id)
	historyDir := filepath.Join(dir, "history")
	if _, err := os.Stat(historyDir); err != nil {
		if os.IsNotExist(err) {
			return s.markLegacyMigrated(ctx, id)
		}
		return err
	}
	if _, err := s.db.GetConversationState(ctx, id, legacyMigratedKey); err == nil {
		return nil
	} else if err != state.ErrNotFound {
		return err
	}
	if err := s.db.SetConversationState(
		ctx, id, legacyMigratingKey, []byte("1"),
	); err != nil {
		return err
	}

	meta, err := readLegacyMeta(dir)
	if err != nil {
		return err
	}
	usageJSON, _ := json.Marshal(meta.Usage)
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
	conv := state.Conversation{
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
	if err := s.db.EnsureConversation(ctx, conv); err != nil {
		return err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var turn TurnRecord
		if err := json.Unmarshal(data, &turn); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
		msgs := filterArchive(turn.Messages)
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
		artifacts, _ := json.Marshal(turn.Artifacts)
		archiveMsgs := make([]state.ArchiveMessage, 0, len(msgs))
		for _, m := range msgs {
			archiveMsgs = append(archiveMsgs, state.ArchiveMessage{
				Role:    string(m.Role),
				Content: m.Content,
			})
		}
		if err := s.db.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
			RunID:         runID,
			At:            at,
			RequestedAt:   requested,
			StartedAt:     started,
			FinishedAt:    finished,
			ArtifactsJSON: artifacts,
		}, archiveMsgs); err != nil {
			return err
		}
	}
	if err := s.importLegacyStateDocs(ctx, dir, id); err != nil {
		return err
	}
	fresh, err := s.db.Conversation(ctx, id)
	if err != nil {
		return err
	}
	if meta.Title != "" {
		fresh.Title = meta.Title
	}
	if len(usageJSON) > 0 && string(usageJSON) != "{}" {
		fresh.UsageJSON = usageJSON
	}
	if meta.ImportSource != "" {
		fresh.ImportSource = meta.ImportSource
		fresh.ImportReady = meta.ImportReady
	}
	if err := s.db.UpsertConversation(ctx, fresh); err != nil {
		return err
	}
	if err := s.markLegacyMigrated(ctx, id); err != nil {
		return err
	}
	_ = os.RemoveAll(historyDir)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
	return nil
}

func (s *Store) markLegacyMigrated(ctx context.Context, id string) error {
	return s.db.SetConversationState(ctx, id, legacyMigratedKey, []byte("1"))
}

func readLegacyMeta(dir string) (legacyMeta, error) {
	var meta legacyMeta
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

func (s *Store) importLegacyStateDocs(
	ctx context.Context, dir, id string,
) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
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
			return err
		}
		if err := s.db.SetConversationState(ctx, id, name, data); err != nil {
			return err
		}
	}
	return nil
}
