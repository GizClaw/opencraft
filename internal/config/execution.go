package config

import (
	"fmt"

	configutils "github.com/GizClaw/flowcraft/sdk/config/utils"
)

// Execution modes.
const (
	ModeRemote = "remote"
	ModeLocal  = "local"
)

// ExecutionConfig is the user-facing execution environment document
// (execution.yaml): which backend exec tools use.
type ExecutionConfig struct {
	Version   string `json:"version"`
	Execution struct {
		Mode      string `json:"mode"`
		ServerURL string `json:"server_url"`
	} `json:"execution"`
}

// ParseExecution strictly decodes the execution document.
func ParseExecution(data []byte) (ExecutionConfig, error) {
	cfg, err := configutils.Decode[ExecutionConfig](data)
	if err != nil {
		return ExecutionConfig{}, err
	}
	switch cfg.Execution.Mode {
	case ModeRemote, ModeLocal:
	default:
		return ExecutionConfig{}, fmt.Errorf(
			"execution.yaml: mode %q must be %q or %q",
			cfg.Execution.Mode, ModeRemote, ModeLocal)
	}
	if cfg.Execution.Mode == ModeRemote && cfg.Execution.ServerURL != "" {
		// Remote URL transport is not implemented yet; fail loudly rather
		// than silently falling back to self-fork.
		return ExecutionConfig{}, fmt.Errorf(
			"execution.yaml: remote server_url is not implemented yet")
	}
	return cfg, nil
}
