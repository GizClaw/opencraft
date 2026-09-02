package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
)

const (
	maxImportSourceBytes  = 512
	maxImportTitleBytes   = 1024
	maxImportTurnCount    = 10000
	maxImportMessageCount = 200000
)

// ImportRequest is the neutral session-import payload. It intentionally
// carries no Codex/source-specific session type; the bundle writer
// converts an external conversation into canonical roles/content.
type ImportRequest struct {
	Title  string       `json:"title"`
	Source string       `json:"source"`
	Turns  []ImportTurn `json:"turns"`
}

// ImportTurn is one archived turn of an imported conversation. At is
// preserved when set and falls back to the import time otherwise.
type ImportTurn struct {
	At       time.Time         `json:"at"`
	Messages []message.Message `json:"messages"`
}

// Import writes a new conversation from a neutral request. It validates
// the request, persists history files and meta.json, and returns the
// generated s-xxx id. The same Source always maps to the same session
// id; a duplicate call while the first import is still seeding memory
// also returns that id. Callers that seed memory must serialize the
// Store.Import + seed + CompleteImport sequence so only one caller
// performs the seed.
//
// Store.Import intentionally does not seed memory; callers should run
// the memory seed next and then CompleteImport. If the seed fails they
// must call AbortImport so neither history nor memory rows survive.
func (s *Store) Import(ctx context.Context, req ImportRequest) (string, error) {
	if err := validateImportRequest(req); err != nil {
		return "", err
	}
	source := strings.TrimSpace(req.Source)

	s.mu.Lock()
	defer s.mu.Unlock()

	if id, meta, err := s.findImportLocked(source); err != nil {
		return "", err
	} else if id != "" {
		if meta.ImportReady {
			return id, nil
		}
		if _, active := s.importPending[id]; active {
			// The first import is still seeding memory. Return the same
			// id; the host-level import mutex prevents a second caller
			// from seeding it again.
			return id, nil
		}
		// A pending directory left by a previous process is stale (this
		// store has no in-flight import for it). Clean it and import
		// again so a crash mid-seed is recoverable.
		if err := s.removeLocked(ctx, id); err != nil {
			return "", err
		}
	}

	// Validate against canonical message rules before persisting.
	var archives []struct {
		At       time.Time
		Messages []message.Message
	}
	now := time.Now().UTC()
	for _, turn := range req.Turns {
		for _, m := range turn.Messages {
			if len(m.Content.Parts) > 0 {
				if err := m.Validate(); err != nil {
					return "", errdefs.Validationf(
						"sessions: import message: %w", err)
				}
			}
		}
		msgs := filterArchive(turn.Messages)
		if len(msgs) == 0 {
			continue
		}
		at := turn.At
		if at.IsZero() {
			at = now
		} else if !at.Equal(at.UTC()) {
			at = at.UTC()
		}
		archives = append(archives, struct {
			At       time.Time
			Messages []message.Message
		}{At: at, Messages: msgs})
	}
	if len(archives) == 0 {
		return "", errdefs.Validationf(
			"sessions: import contains no archiveable messages")
	}

	id := NewID()
	dir := s.dir(id)
	historyDir := filepath.Join(dir, "history")
	if err := os.MkdirAll(historyDir, 0o700); err != nil {
		return "", err
	}
	_ = os.Chmod(dir, 0o700)
	_ = os.Chmod(historyDir, 0o700)

	seq := 1
	messageCount := 0
	createdAt := archives[0].At
	updatedAt := archives[len(archives)-1].At
	for i, arch := range archives {
		file := TurnRecord{
			Seq:      seq + i,
			At:       arch.At,
			Messages: arch.Messages,
		}
		messageCount += len(arch.Messages)
		data, err := json.MarshalIndent(file, "", "  ")
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		path := filepath.Join(historyDir, fmt.Sprintf("%06d.json", seq+i))
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, data, 0o600); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.RemoveAll(dir)
			return "", err
		}
	}
	s.seqCache[id] = seq + len(archives)

	meta := sessionMeta{
		TurnCount:    len(archives),
		MessageCount: messageCount,
		Title:        strings.TrimSpace(req.Title),
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
		ImportSource: source,
	}
	if meta.Title == "" {
		for _, arch := range archives {
			for _, m := range arch.Messages {
				if m.Role != message.RoleUser {
					continue
				}
				meta.Title = firstLine(m.Content.Text())
				if meta.Title == "" && len(m.Content.Parts) > 0 {
					meta.Title = "[attachment]"
				}
				if meta.Title != "" {
					break
				}
			}
			if meta.Title != "" {
				break
			}
		}
	}
	if meta.Title == "" {
		meta.Title = "(imported)"
	}
	if err := s.writeMeta(id, meta); err != nil {
		_ = s.removeLocked(ctx, id)
		return "", err
	}
	s.importPending[id] = source
	return id, nil
}

// CompleteImport marks an imported session as memory-seeded and visible
// in the session list. It is a no-op when the session is already
// complete.
func (s *Store) CompleteImport(ctx context.Context, id string) error {
	if err := requireID(id); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	meta, err := s.loadMeta(id)
	if err != nil {
		return err
	}
	if meta.ImportSource == "" {
		return errdefs.Validationf(
			"sessions: session %s is not an import", id)
	}
	if meta.ImportReady {
		return nil
	}
	meta.ImportReady = true
	delete(s.importPending, id)
	return s.writeMeta(id, meta)
}

// ImportReady reports whether an imported session has completed its
// memory seed.
func (s *Store) ImportReady(_ context.Context, id string) (bool, error) {
	if err := requireID(id); err != nil {
		return false, err
	}
	meta, err := s.loadMeta(id)
	if err != nil {
		return false, err
	}
	return meta.ImportSource != "" && meta.ImportReady, nil
}

// AbortImport rolls back an import that failed before CompleteImport:
// it removes the session directory and every memory/settings row for
// the session.
func (s *Store) AbortImport(ctx context.Context, id string) error {
	return s.Remove(ctx, id)
}

func validateImportRequest(req ImportRequest) error {
	source := strings.TrimSpace(req.Source)
	if source == "" {
		return errdefs.Validationf("sessions: import source is required")
	}
	if len(source) > maxImportSourceBytes {
		return errdefs.Validationf(
			"sessions: import source exceeds %d bytes", maxImportSourceBytes)
	}
	if strings.ContainsRune(source, '\x00') {
		return errdefs.Validationf("sessions: import source must not contain NUL")
	}
	if len(req.Title) > maxImportTitleBytes {
		return errdefs.Validationf(
			"sessions: import title exceeds %d bytes", maxImportTitleBytes)
	}
	if len(req.Turns) == 0 {
		return errdefs.Validationf("sessions: import turns are required")
	}
	if len(req.Turns) > maxImportTurnCount {
		return errdefs.Validationf(
			"sessions: import exceeds %d turns", maxImportTurnCount)
	}
	total := 0
	for i, turn := range req.Turns {
		total += len(turn.Messages)
		if total > maxImportMessageCount {
			return errdefs.Validationf(
				"sessions: import exceeds %d messages", maxImportMessageCount)
		}
		for _, m := range turn.Messages {
			if m.Role == "" && len(m.Content.Parts) == 0 {
				continue
			}
			if m.Role == "" {
				return errdefs.Validationf(
					"sessions: import turn %d has a message without role", i+1)
			}
		}
	}
	if total == 0 {
		return errdefs.Validationf("sessions: import has no messages")
	}
	return nil
}

func (s *Store) findImportLocked(source string) (string, sessionMeta, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", sessionMeta{}, nil
		}
		return "", sessionMeta{}, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "s-") {
			continue
		}
		id := entry.Name()
		meta, err := s.loadMeta(id)
		if err != nil {
			return "", sessionMeta{}, err
		}
		if meta.ImportSource == source {
			return id, meta, nil
		}
	}
	return "", sessionMeta{}, nil
}
