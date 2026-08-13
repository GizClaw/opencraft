package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/GizClaw/flowcraft/core/agent"
	"github.com/GizClaw/flowcraft/core/message"
)

// blockKind discriminates one rendered message block.
type blockKind int

const (
	blockUser blockKind = iota
	blockAssistant
	blockReasoning
	blockTool
)

type block struct {
	kind     blockKind
	text     string
	name     string
	args     string
	result   string
	isErr    bool
	rendered string
}

// ChatState is the pure conversation state: message blocks and their
// rendering. It has no bubbletea dependencies.
type ChatState struct {
	blocks []*block
}

// AppendUser adds a user message block.
func (s *ChatState) AppendUser(text string) {
	s.blocks = append(s.blocks, &block{kind: blockUser, text: text})
}

// AppendAssistant opens a new assistant block for streaming.
func (s *ChatState) AppendAssistant() {
	s.blocks = append(s.blocks, &block{kind: blockAssistant})
}

// AppendDelta applies one stream delta to the state. flowcraft core
// streams every part (text, reasoning, tool call, tool result) in run
// order as StreamDeltaPart.
func (s *ChatState) AppendDelta(delta agent.StreamDeltaPayload) {
	if delta.Type != agent.StreamDeltaPart || delta.Part == nil {
		return
	}
	switch p := delta.Part.(type) {
	case message.TextPart:
		if len(s.blocks) == 0 ||
			s.blocks[len(s.blocks)-1].kind != blockAssistant {
			s.blocks = append(s.blocks, &block{kind: blockAssistant})
		}
		last := s.blocks[len(s.blocks)-1]
		last.text += p.Text
		last.rendered = ""
	case message.ReasoningPart:
		if last := s.lastReasoning(); last != nil {
			last.text += p.Text
			last.rendered = ""
			return
		}
		s.blocks = append(s.blocks, &block{kind: blockReasoning, text: p.Text})
	case message.ToolCallPart:
		args := string(p.Call.Arguments)
		display := truncate(args, 400)
		if p.Call.Name == "exec_command" {
			var a struct {
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(args), &a) == nil && a.Command != "" {
				display = truncate(a.Command, 400)
			}
		}
		s.blocks = append(s.blocks, &block{
			kind: blockTool,
			name: p.Call.Name,
			args: display,
		})
	case message.ToolResultPart:
		for i := len(s.blocks) - 1; i >= 0; i-- {
			if s.blocks[i].kind == blockTool && s.blocks[i].result == "" {
				content := p.Result.Content
				if s.blocks[i].name == "exec_command" {
					var r struct {
						ExitCode int    `json:"exit_code"`
						Stdout   string `json:"stdout"`
						Stderr   string `json:"stderr"`
					}
					if json.Unmarshal([]byte(content), &r) == nil {
						content = formatExecResult(r.ExitCode, r.Stdout, r.Stderr)
					}
				}
				s.blocks[i].result = truncate(content, 800)
				s.blocks[i].isErr = p.Result.IsError
				break
			}
		}
	}
}

func (s *ChatState) lastReasoning() *block {
	for i := len(s.blocks) - 1; i >= 0; i-- {
		if s.blocks[i].kind == blockReasoning {
			return s.blocks[i]
		}
		return nil
	}
	return nil
}

// formatExecResult renders an exec_command result for the UI, omitting
// output sections that are empty.
func formatExecResult(exitCode int, stdout, stderr string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit_code=%d", exitCode)
	if stderr != "" {
		fmt.Fprintf(&b, "\n[stderr]\n%s", stderr)
	}
	if stdout != "" {
		fmt.Fprintf(&b, "\n[stdout]\n%s", stdout)
	}
	return b.String()
}

// AppendReply records a user's modal reply as a tool block.
func (s *ChatState) AppendReply(text string) {
	s.blocks = append(s.blocks, &block{
		kind: blockTool, name: "user reply", result: truncate(text, 400),
	})
}

// AppendError records a turn error.
func (s *ChatState) AppendError(err error) {
	if err != nil {
		s.blocks = append(s.blocks, &block{
			kind: blockTool, name: "error", result: err.Error(), isErr: true,
		})
	}
}

func (s *ChatState) lastAssistant() *block {
	for i := len(s.blocks) - 1; i >= 0; i-- {
		if s.blocks[i].kind == blockAssistant {
			return s.blocks[i]
		}
	}
	return nil
}

// Render renders all blocks as viewport content.
func (s *ChatState) Render() string {
	var parts []string
	for _, b := range s.blocks {
		if rendered, ok := b.render(); ok {
			parts = append(parts, rendered)
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (b *block) render() (string, bool) {
	switch b.kind {
	case blockUser:
		return userStyle.Render("> " + b.text), true
	case blockAssistant:
		if b.rendered == "" {
			text := strings.TrimSpace(b.text)
			if text == "" {
				return "", false
			}
			rendered, err := markdownRenderer.Render(text)
			if err != nil || rendered == "" {
				rendered = assistantStyle.Render(text)
			}
			b.rendered = strings.TrimSpace(rendered)
		}
		if b.rendered == "" {
			return "", false
		}
		return assistantStyle.Render(b.rendered), true
	case blockReasoning:
		return reasoningStyle.Render(strings.TrimSpace(b.text)), true
	case blockTool:
		title := "⚙ " + b.name
		style := toolTitleStyle
		if b.isErr {
			style = toolErrStyle
		}
		lines := []string{style.Render(title)}
		if b.args != "" {
			lines = append(lines, "  args: "+truncate(b.args, 200))
		}
		if b.result != "" {
			mark := "✓"
			markStyle := toolOKStyle
			if b.isErr {
				mark = "✗"
				markStyle = toolErrStyle
			}
			lines = append(lines, markStyle.Render("  "+mark+" "+truncate(b.result, 400)))
		}
		return lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240")).
			Padding(0, 1).
			Render(lipgloss.JoinVertical(lipgloss.Left, lines...)), true
	default:
		return "", false
	}
}

// markdownRenderer renders assistant text with a fixed dark style.
// The "auto" style would query the terminal background color on every
// render, and those responses leak into the UI's keyboard stream while
// tea is running. A fixed style never queries.
var markdownRenderer = func() *glamour.TermRenderer {
	r, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	if err != nil {
		panic("tui: create markdown renderer: " + err.Error())
	}
	return r
}()
