package tools

import (
	"encoding/json"

	"github.com/GizClaw/flowcraft/core/resource"
	"github.com/GizClaw/flowcraft/core/tool"
)

// toolList adapts a fixed []tool.Tool to tool.Source.
type toolList []tool.Tool

func (l toolList) Tools() []tool.Tool { return l }

func (toolList) LazyTools() []tool.LazyTool { return nil }

var _ tool.Source = toolList(nil)

// sourceEnabled reads the optional enabled switch from a source's
// settings. Absent means enabled.
func sourceEnabled(in resource.Input) bool {
	if len(in.Settings) == 0 {
		return true
	}
	var s struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(in.Settings, &s); err != nil || s.Enabled == nil {
		return true
	}
	return *s.Enabled
}
