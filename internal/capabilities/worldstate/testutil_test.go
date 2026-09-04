package worldstate

import "github.com/GizClaw/flowcraft/core/workspace"

func NewLocalWorkspaceForTest(root string) (workspace.Workspace, error) {
	return workspace.NewLocalWorkspace(root)
}
