package sessions

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/errdefs"
	"github.com/GizClaw/flowcraft/core/message"
	"github.com/GizClaw/flowcraft/core/message/media"

	"github.com/GizClaw/opencraft/internal/capabilities/sessions/state"
)

// ForkResult describes one newly forked conversation. Turns contains
// the archived messages copied into the new session (with attachment
// paths rewritten to the fork's own media/files directories), so the
// caller can seed memory from exactly the fork point onward.
type ForkResult struct {
	ID    string
	Turns []TurnRecord
}

// Fork copies every archived turn through sourceRunID into a fresh
// session and returns its id. The fork keeps mode/think/model and the
// source's custom title; user attachment files are copied into the new
// session directory so deleting the source later does not break the
// fork. Memory rows are deliberately not copied here: orchestration/host
// seeds them after the archive transaction so the model continues with
// exactly the forked prefix.
func (s *Store) Fork(
	ctx context.Context, sourceID, sourceRunID string,
) (ForkResult, error) {
	if err := requireID(sourceID); err != nil {
		return ForkResult{}, err
	}
	if strings.TrimSpace(sourceRunID) == "" {
		return ForkResult{}, errdefs.Validationf(
			"sessions: fork source run id is required")
	}
	turns, err := s.Turns(ctx, sourceID)
	if err != nil {
		return ForkResult{}, fmt.Errorf("sessions: fork load %s: %w", sourceID, err)
	}
	targetIdx := -1
	for i := range turns {
		if turns[i].RunID == sourceRunID {
			targetIdx = i
			break
		}
	}
	if targetIdx < 0 {
		return ForkResult{}, errdefs.Validationf(
			"sessions: fork source run %q not found in %s", sourceRunID, sourceID)
	}
	if status := turns[targetIdx].Status; status != "" && status != "completed" {
		return ForkResult{}, errdefs.Validationf(
			"sessions: cannot fork %s turn %q (status %q)",
			sourceID, sourceRunID, status)
	}

	newID, err := s.Create()
	if err != nil {
		return ForkResult{}, fmt.Errorf("sessions: fork create: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = s.Remove(ctx, newID)
		}
	}()

	copied := make([]TurnRecord, 0, targetIdx+1)
	attachmentCache := make(map[string]string)
	for _, sourceTurn := range turns[:targetIdx+1] {
		archiveMsgs := make([]state.ArchiveMessage, 0, len(sourceTurn.Messages))
		for _, m := range sourceTurn.Messages {
			content, err := s.forkMessageContent(
				newID, m.Content, attachmentCache,
			)
			if err != nil {
				return ForkResult{}, fmt.Errorf(
					"sessions: fork attachment into %s: %w", newID, err)
			}
			archiveMsgs = append(archiveMsgs, state.ArchiveMessage{
				Role:    string(m.Role),
				Content: content,
			})
		}
		conv := state.Conversation{ID: newID}
		if len(copied) == 0 {
			conv.Title = firstArchiveTitle(sourceTurn.Messages)
		}
		artifacts := []byte("[]")
		if len(sourceTurn.Artifacts) > 0 {
			if raw, err := json.Marshal(sourceTurn.Artifacts); err != nil {
				return ForkResult{}, fmt.Errorf(
					"sessions: fork artifacts: %w", err)
			} else {
				artifacts = raw
			}
		}
		if err := s.db.CommitConversationTurn(ctx, conv, state.ArchiveTurn{
			RunID:         sourceTurn.RunID,
			At:            sourceTurn.At,
			RequestedAt:   sourceTurn.RequestedAt,
			StartedAt:     sourceTurn.StartedAt,
			FinishedAt:    sourceTurn.FinishedAt,
			Status:        sourceTurn.Status,
			Error:         sourceTurn.Error,
			ArtifactsJSON: artifacts,
		}, archiveMsgs); err != nil {
			return ForkResult{}, fmt.Errorf(
				"sessions: fork commit %s into %s: %w",
				sourceTurn.RunID, newID, err)
		}
		forkedTurn := sourceTurn
		forkedTurn.Messages = make([]message.Message, 0, len(archiveMsgs))
		for _, archived := range archiveMsgs {
			forkedTurn.Messages = append(
				forkedTurn.Messages,
				message.Message{
					Role:    message.Role(archived.Role),
					Content: archived.Content,
				},
			)
		}
		copied = append(copied, forkedTurn)
	}

	if err := s.copyForkSettings(ctx, sourceID, newID); err != nil {
		return ForkResult{}, fmt.Errorf("sessions: fork settings: %w", err)
	}
	cleanup = false
	return ForkResult{ID: newID, Turns: copied}, nil
}

func (s *Store) copyForkSettings(
	ctx context.Context, sourceID, newID string,
) error {
	mode, err := s.Mode(ctx, sourceID)
	if err != nil {
		return err
	}
	if err := s.SetMode(ctx, newID, mode); err != nil {
		return err
	}
	think, err := s.Think(ctx, sourceID)
	if err != nil {
		return err
	}
	if err := s.SetThink(ctx, newID, think); err != nil {
		return err
	}
	model, err := s.Model(ctx, sourceID)
	if err != nil {
		return err
	}
	if err := s.SetModel(ctx, newID, model); err != nil {
		return err
	}
	var customTitle string
	if err := s.ReadState(sourceID, "title", &customTitle); err == nil &&
		strings.TrimSpace(customTitle) != "" {
		return s.WriteState(newID, "title", customTitle)
	}
	return nil
}

// forkMessageContent copies any local attachment files referenced by
// the source message into the new session and rewrites their paths.
// Remote URLs, inline bytes, and already-missing local files pass
// through untouched so a stale attachment never blocks forking.
func (s *Store) forkMessageContent(
	newID string,
	content message.Content,
	cache map[string]string,
) (message.Content, error) {
	if len(content.Parts) == 0 {
		return content, nil
	}
	out := make([]message.Part, 0, len(content.Parts))
	for _, raw := range content.Parts {
		part, err := message.NormalizePart(raw)
		if err != nil {
			return message.Content{}, err
		}
		switch p := part.(type) {
		case message.ImagePart:
			if p.Source.Kind() == media.SourceURL {
				if src, ok := forkLocalFilePath(p.Source.URL()); ok {
					dst, copied, err := s.copyForkAttachment(
						newID, "media", src, p.Source.MediaType(), cache,
					)
					if err != nil {
						return message.Content{}, err
					}
					if copied {
						p.Source, err = forkImageSource(
							dst, mediaTypeOr(dst, p.Source.MediaType()),
						)
						if err != nil {
							return message.Content{}, err
						}
					}
				}
			}
			out = append(out, p)
		case message.AudioPart:
			if p.Source.Kind() == media.SourceURL {
				if src, ok := forkLocalFilePath(p.Source.URL()); ok {
					dst, copied, err := s.copyForkAttachment(
						newID, "media", src, p.Source.MediaType(), cache,
					)
					if err != nil {
						return message.Content{}, err
					}
					if copied {
						p.Source, err = forkAudioSource(
							dst, mediaTypeOr(dst, p.Source.MediaType()),
						)
						if err != nil {
							return message.Content{}, err
						}
					}
				}
			}
			out = append(out, p)
		case message.VideoPart:
			if p.Source.Kind() == media.SourceURL {
				if src, ok := forkLocalFilePath(p.Source.URL()); ok {
					dst, copied, err := s.copyForkAttachment(
						newID, "media", src, p.Source.MediaType(), cache,
					)
					if err != nil {
						return message.Content{}, err
					}
					if copied {
						p.Source, err = forkVideoSource(
							dst, mediaTypeOr(dst, p.Source.MediaType()),
						)
						if err != nil {
							return message.Content{}, err
						}
					}
				}
			}
			out = append(out, p)
		case message.FilePart:
			if src, ok := forkLocalFilePath(p.URI); ok {
				dst, copied, err := s.copyForkAttachment(
					newID, "files", src, p.MediaType, cache,
				)
				if err != nil {
					return message.Content{}, err
				}
				if copied {
					p.URI = dst
					p.MediaType = mediaTypeOr(dst, p.MediaType)
				}
			}
			out = append(out, p)
		default:
			out = append(out, p)
		}
	}
	return message.Content{Parts: out}, nil
}

func (s *Store) copyForkAttachment(
	newID, kind, src, mediaType string,
	cache map[string]string,
) (string, bool, error) {
	if dst := cache[src]; dst != "" {
		return dst, true, nil
	}
	dst, err := s.SaveAttachment(newID, kind, src)
	if err != nil {
		return "", false, fmt.Errorf("copy %s attachment %q: %w", kind, src, err)
	}
	cache[src] = dst
	return dst, true, nil
}

// forkLocalFilePath resolves a part URL/URI to an existing regular
// file. Remote/data sources and missing files return ok=false so they
// stay untouched.
func forkLocalFilePath(raw string) (string, bool) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return "", false
	}
	path = strings.TrimPrefix(path, "file://")
	if strings.HasPrefix(path, "http://") ||
		strings.HasPrefix(path, "https://") ||
		strings.HasPrefix(path, "data:") {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func mediaTypeOr(path, mediaType string) string {
	if mediaType != "" {
		return mediaType
	}
	if t := mime.TypeByExtension(filepath.Ext(path)); t != "" {
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		return strings.TrimSpace(t)
	}
	return ""
}

func forkImageSource(path, mediaType string) (media.ImageSource, error) {
	var source media.ImageSource
	if err := unmarshalForkSource(path, mediaType, &source); err != nil {
		return media.ImageSource{}, err
	}
	return source, nil
}

func forkAudioSource(path, mediaType string) (media.AudioSource, error) {
	var source media.AudioSource
	if err := unmarshalForkSource(path, mediaType, &source); err != nil {
		return media.AudioSource{}, err
	}
	return source, nil
}

func forkVideoSource(path, mediaType string) (media.VideoSource, error) {
	var source media.VideoSource
	if err := unmarshalForkSource(path, mediaType, &source); err != nil {
		return media.VideoSource{}, err
	}
	return source, nil
}

func unmarshalForkSource(path, mediaType string, target any) error {
	raw, err := json.Marshal(struct {
		Kind      string `json:"kind"`
		URL       string `json:"url"`
		MediaType string `json:"media_type,omitempty"`
	}{Kind: "url", URL: path, MediaType: mediaType})
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}
