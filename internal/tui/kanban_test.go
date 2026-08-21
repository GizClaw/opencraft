package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/GizClaw/flowcraft/core/delegation"
	"github.com/GizClaw/flowcraft/core/delegation/kanban"
)

func testCard(status kanban.Status, target, input string, result *kanban.Result) *kanban.Card {
	now := time.Now()
	return &kanban.Card{
		ID:        "d-test",
		Status:    status,
		Task:      &kanban.Task{Request: delegation.AsyncRequest{Request: delegation.Request{Target: target, Input: input}}},
		Result:    result,
		CreatedAt: now.Add(-time.Minute),
		UpdatedAt: now,
	}
}

func TestEnterKanbanModeAndClose(t *testing.T) {
	m := newTestModel()
	m.width = 80
	updated, cmd := m.enterKanbanMode()
	next := updated.(*Model)
	if next.mode != modeKanban {
		t.Fatalf("mode = %v, want modeKanban", next.mode)
	}
	if cmd == nil {
		t.Fatal("enterKanbanMode should arm the refresh tick")
	}
	// Esc returns to the pre-overlay mode (idle in the test model).
	after, _ := next.handleKanbanKey(tea.KeyMsg{Type: tea.KeyEsc})
	if after.(*Model).mode != modeIdle {
		t.Fatalf("after esc mode = %v, want modeIdle", after.(*Model).mode)
	}
}

func TestKanbanTickRefreshesWhileOpen(t *testing.T) {
	m := newTestModel()
	updated, _ := m.enterKanbanMode()
	next := updated.(*Model)
	// A live refresh re-arms the tick while the overlay stays open.
	refreshed, cmd := next.Update(kanbanTickMsg{})
	if cmd == nil || refreshed.(*Model).mode != modeKanban {
		t.Fatalf("tick while open: cmd=%v mode=%v", cmd, refreshed.(*Model).mode)
	}
	next.leaveKanban()
	if m := next; m.mode != modeIdle {
		t.Fatalf("after leave mode = %v, want modeIdle", m.mode)
	}
	afterLeave, cmd := next.Update(kanbanTickMsg{})
	if cmd != nil || afterLeave.(*Model).mode != modeIdle {
		t.Fatalf("tick after leave: cmd=%v mode=%v", cmd, afterLeave.(*Model).mode)
	}
}

func TestKanbanViewRendersCards(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.height = 40
	m.kanban.cards = []*kanban.Card{
		testCard(kanban.StatusDone, "assistant",
			"review the diff",
			&kanban.Result{Response: delegation.Response{
				Status: delegation.StatusSucceeded,
				Output: "looks good",
			}}),
		testCard(kanban.StatusFailed, "assistant",
			"bump deps",
			&kanban.Result{Response: delegation.Response{
				Status: delegation.StatusFailed,
				Error:  "network error",
			}}),
	}
	v := m.kanbanView()
	for _, want := range []string{"subagents", "assistant", "review the diff", "bump deps", "looks good", "network error"} {
		if !strings.Contains(v, want) {
			t.Errorf("kanban view missing %q:\n%s", want, v)
		}
	}
}

func TestKanbanViewEmptyStates(t *testing.T) {
	m := newTestModel()
	if got := m.kanbanView(); !strings.Contains(got, "delegation 未启用") {
		t.Errorf("unwired board should show disabled state: %q", got)
	}
}

func TestKanbanScroll(t *testing.T) {
	m := newTestModel()
	m.width = 80
	m.kanban.cards = []*kanban.Card{
		testCard(kanban.StatusPending, "assistant", "one", nil),
		testCard(kanban.StatusPending, "assistant", "two", nil),
	}
	m.kanban.scroll = 0
	m.height = 5 // one visible card row budget
	after, _ := m.handleKanbanKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	if after.(*Model).kanban.scroll <= 0 {
		t.Fatal("down key should scroll the board")
	}
}
