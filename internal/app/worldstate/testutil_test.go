package worldstate

import "github.com/GizClaw/flowcraft/sdk/workspace"

func NewLocalWorkspaceForTest(root string) (workspace.Workspace, error) {
	return workspace.NewLocalWorkspace(root)
}
