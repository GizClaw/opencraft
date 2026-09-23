package bindings

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GizClaw/flowcraft/core/errdefs"

	"github.com/GizClaw/opencraft/internal/adapters/desktop/core"
	"github.com/GizClaw/opencraft/internal/capabilities/memory/userstore"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/orchestration/host"
)

// User-level long-term memory as the memory tab consumes it. The store
// (capabilities/memory/userstore) stays the single write point for
// facts: this layer only maps rows into DTOs, resolves which workspace a
// workspace-scoped fact belongs to, and performs the settings write.
// Without a user database the reads degrade to an empty list instead of
// failing, because the card is rendered before a workspace exists.

// MemoryFactView is one stored user-level fact.
type MemoryFactView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	// Workspace is the owning workspace path for a workspace-scoped
	// fact, empty for a global one. Names are resolved in the page, so
	// a deleted workspace still renders as the path it was.
	Workspace          string `json:"workspace,omitempty"`
	Text               string `json:"text"`
	SourceConversation string `json:"source_conversation,omitempty"`
	SourceRun          string `json:"source_run,omitempty"`
	CreatedAt          string `json:"created_at,omitempty"`
	UpdatedAt          string `json:"updated_at,omitempty"`
	Stale              bool   `json:"stale"`
}

// UserMemoryState is the whole state of the long-term memory card: the
// effective settings, the read-only bounds the store enforces, and how
// many facts the current workspace currently sees.
type UserMemoryState struct {
	Enabled        bool `json:"enabled"`
	InjectMaxItems int  `json:"inject_max_items"`
	InjectMaxChars int  `json:"inject_max_chars"`
	// The editable range of each budget, so the card clamps in the same
	// direction the backend validates.
	MinInjectMaxItems     int `json:"min_inject_max_items"`
	MaxInjectMaxItems     int `json:"max_inject_max_items"`
	DefaultInjectMaxItems int `json:"default_inject_max_items"`
	MinInjectMaxChars     int `json:"min_inject_max_chars"`
	MaxInjectMaxChars     int `json:"max_inject_max_chars"`
	DefaultInjectMaxChars int `json:"default_inject_max_chars"`
	// MaxItems/MaxTextBytes are the store's hard rails. They are not
	// user preferences: the card shows them read-only.
	MaxItems     int `json:"max_items"`
	MaxTextBytes int `json:"max_text_bytes"`
	// Available reports whether a user database is open. When it is
	// false the card still renders the settings and disables writes.
	Available bool `json:"available"`
	// Workspace is the workspace the counts are scoped to, empty when
	// no workspace is open (only global facts are counted then).
	Workspace string `json:"workspace,omitempty"`
	// Live and Stale count the facts visible from Workspace.
	Live  int `json:"live"`
	Stale int `json:"stale"`
}

// MemoryFactRequest is the payload of one new fact.
type MemoryFactRequest struct {
	Text string `json:"text"`
	// Scope is "global" or "workspace"; empty picks the narrower one
	// that fits (workspace when a workspace is open).
	Scope string `json:"scope,omitempty"`
	// Workspace overrides the active workspace for a workspace-scoped
	// fact.
	Workspace string `json:"workspace,omitempty"`
	Kind      string `json:"kind,omitempty"`
}

// UserMemorySettingsRequest is the save payload of the card.
type UserMemorySettingsRequest struct {
	Enabled        bool `json:"enabled"`
	InjectMaxItems int  `json:"inject_max_items"`
	InjectMaxChars int  `json:"inject_max_chars"`
}

// memoryFactView maps one stored fact to the wire shape. Timestamps go
// out as RFC3339 so the page formats them in the user's locale instead
// of receiving a Go time.
func memoryFactView(fact userstore.Fact) MemoryFactView {
	out := MemoryFactView{
		ID:                 fact.ID,
		Kind:               fact.Kind,
		Scope:              fact.Scope,
		Workspace:          fact.Workspace,
		Text:               fact.Text,
		SourceConversation: fact.SourceConversation,
		SourceRun:          fact.SourceRun,
		Stale:              fact.Stale,
		CreatedAt:          rfc3339(fact.CreatedAt),
		UpdatedAt:          rfc3339(fact.UpdatedAt),
	}
	return out
}

// rfc3339 renders a timestamp for the UI, empty for the zero time (a
// never-used skill has no "last used", and the page shows an em dash
// rather than 1970).
func rfc3339(at time.Time) string {
	if at.IsZero() {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

// memoryStoreOf resolves the user-level memory store out of a core, or
// false when this runtime has no user database.
func memoryStoreOf(c *core.Core) (userstore.Memory, bool) {
	manager := c.Runtime.Manager()
	if manager == nil {
		return nil, false
	}
	store := manager.MemoryStore()
	if store == nil || store.Empty() {
		return nil, false
	}
	return store, true
}

// requireMemoryStoreOf is the write-side guard: a write without a user
// database is refused instead of silently dropped. The memory card and
// the review queue's accept path share it, so both refuse the same way.
func requireMemoryStoreOf(c *core.Core) (userstore.Memory, error) {
	store, ok := memoryStoreOf(c)
	if !ok {
		return nil, errdefs.NotAvailablef(
			"user memory: no user database in this runtime")
	}
	return store, nil
}

// memoryQuery is the read scope of one listing: the facts visible from
// the active workspace, stale ones included so the card can show them
// marked instead of hiding them.
func (b *Config) memoryQuery() userstore.Query {
	return userstore.Query{
		Workspace:    b.core.ActiveWorkDir(),
		IncludeStale: true,
	}
}

// UserMemoryState returns the effective long-term memory settings plus
// the fact counts of the active workspace. A runtime without a user
// database reports the settings with Available=false and zero counts.
func (b *Config) UserMemoryState() (UserMemoryState, error) {
	loaded, err := config.LoadUserMemory(b.core.UserDir)
	if err != nil {
		return UserMemoryState{}, err
	}
	effective, err := loaded.Resolve()
	if err != nil {
		return UserMemoryState{}, err
	}
	out := UserMemoryState{
		Enabled:               effective.Enabled,
		InjectMaxItems:        effective.InjectMaxItems,
		InjectMaxChars:        effective.InjectMaxChars,
		MinInjectMaxItems:     config.UserMemoryMinInjectMaxItems,
		MaxInjectMaxItems:     config.UserMemoryMaxInjectMaxItems,
		DefaultInjectMaxItems: config.UserMemoryDefaultInjectMaxItems,
		MinInjectMaxChars:     config.UserMemoryMinInjectMaxChars,
		MaxInjectMaxChars:     config.UserMemoryMaxInjectMaxChars,
		DefaultInjectMaxChars: config.UserMemoryDefaultInjectMaxChars,
		MaxItems:              userstore.MaxItems,
		MaxTextBytes:          userstore.MaxTextBytes,
		Workspace:             b.core.ActiveWorkDir(),
	}
	store, ok := memoryStoreOf(b.core)
	if !ok {
		return out, nil
	}
	out.Available = true
	facts, err := store.List(
		b.core.Shell.Context(), b.memoryQuery())
	if err != nil {
		return UserMemoryState{}, err
	}
	for _, fact := range facts {
		if fact.Stale {
			out.Stale++
			continue
		}
		out.Live++
	}
	return out, nil
}

// MemoryFacts lists the facts visible from the active workspace, newest
// first (the store's order). With no user database the list is empty
// rather than an error: the page is reachable before one is opened.
func (b *Config) MemoryFacts() ([]MemoryFactView, error) {
	out := make([]MemoryFactView, 0)
	store, ok := memoryStoreOf(b.core)
	if !ok {
		return out, nil
	}
	facts, err := store.List(
		b.core.Shell.Context(), b.memoryQuery())
	if err != nil {
		return nil, err
	}
	for _, fact := range facts {
		out = append(out, memoryFactView(fact))
	}
	return out, nil
}

// resolveFactScope fills in the scope a request left unset and pairs it
// with the workspace the store must record. A fact about the project
// belongs to the workspace the window is in; only a statement about the
// user or the machine is global.
func (b *Config) resolveFactScope(scope, workspace string) (string, string, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		workspace = b.core.ActiveWorkDir()
	}
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case userstore.ScopeGlobal:
		return userstore.ScopeGlobal, "", nil
	case userstore.ScopeWorkspace:
		if workspace == "" {
			return "", "", errors.New(
				"user memory: a workspace fact needs an open workspace")
		}
		return userstore.ScopeWorkspace, workspace, nil
	case "":
		if workspace == "" {
			return userstore.ScopeGlobal, "", nil
		}
		return userstore.ScopeWorkspace, workspace, nil
	default:
		return "", "", fmt.Errorf(
			"user memory: unknown scope %q (want global or workspace)", scope)
	}
}

// AddMemoryFact stores one new fact through the store's write path, so
// dedupe, the text/byte limits and the provenance all apply.
func (b *Config) AddMemoryFact(req MemoryFactRequest) (MemoryFactView, error) {
	store, err := requireMemoryStoreOf(b.core)
	if err != nil {
		return MemoryFactView{}, err
	}
	scope, workspace, err := b.resolveFactScope(req.Scope, req.Workspace)
	if err != nil {
		return MemoryFactView{}, err
	}
	fact, err := store.Add(b.core.Shell.Context(), userstore.Fact{
		Kind:      req.Kind,
		Scope:     scope,
		Workspace: workspace,
		Text:      req.Text,
	})
	if err != nil {
		return MemoryFactView{}, err
	}
	return memoryFactView(fact), nil
}

// UpdateMemoryFact rewrites one fact's text. Identity (the dedupe key)
// is recomputed by the store, so restating a fact edits the row.
func (b *Config) UpdateMemoryFact(id, text string) (MemoryFactView, error) {
	store, err := requireMemoryStoreOf(b.core)
	if err != nil {
		return MemoryFactView{}, err
	}
	fact, err := store.Replace(b.core.Shell.Context(), id, text)
	if err != nil {
		return MemoryFactView{}, err
	}
	return memoryFactView(fact), nil
}

// RemoveMemoryFact deletes one fact.
func (b *Config) RemoveMemoryFact(id string) error {
	store, err := requireMemoryStoreOf(b.core)
	if err != nil {
		return err
	}
	return store.Remove(b.core.Shell.Context(), id)
}

// SetMemoryFactStale marks a fact stale (kept, but no longer injected)
// or brings it back.
func (b *Config) SetMemoryFactStale(id string, stale bool) (MemoryFactView, error) {
	store, err := requireMemoryStoreOf(b.core)
	if err != nil {
		return MemoryFactView{}, err
	}
	fact, err := store.SetStale(b.core.Shell.Context(), id, stale)
	if err != nil {
		return MemoryFactView{}, err
	}
	return memoryFactView(fact), nil
}

// SaveUserMemorySettings persists the card's settings and reloads the
// document so the worldstate injection picks the new budget up without
// an app restart.
func (b *Config) SaveUserMemorySettings(req UserMemorySettingsRequest) error {
	enabled := req.Enabled
	settings := config.UserMemorySettings{
		Enabled:        &enabled,
		InjectMaxItems: req.InjectMaxItems,
		InjectMaxChars: req.InjectMaxChars,
	}
	if err := config.SaveUserMemory(b.core.UserDir, settings); err != nil {
		return err
	}
	return b.core.ApplyDocumentReload(host.WithAssemblyReason(
		b.core.Shell.Context(), host.ReasonSettingsSave))
}
