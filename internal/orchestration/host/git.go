package host

import (
	"context"
	"os"
	"path/filepath"

	"github.com/GizClaw/opencraft/internal/capabilities/undo"
	"github.com/GizClaw/opencraft/internal/foundation/utils/gitx"
)

func gitSnapshot(ctx context.Context, wd string) []undo.FileState {
	repo := gitx.Root(wd)
	if repo == "" {
		return nil
	}
	paths := gitx.ChangedPaths(ctx, repo, gitx.ChangedOptions{
		MaxBytes: 4 << 20,
		MaxPaths: 2000,
	})
	if len(paths) == 0 {
		return nil
	}
	states := make([]undo.FileState, 0, len(paths))
	for _, rel := range paths {
		abs := filepath.Join(wd, filepath.FromSlash(rel))
		info, err := os.Lstat(abs)
		if err != nil {
			states = append(states, undo.FileState{Path: rel})
			continue
		}
		if info.IsDir() {
			continue
		}
		st := undo.FileState{Path: rel, Present: true}
		if info.Mode()&os.ModeSymlink != 0 {
			st.Kind = undo.KindSymlink
			target, err := os.Readlink(abs)
			if err != nil {
				st.Skipped = true
			} else {
				st.LinkTarget = target
			}
			states = append(states, st)
			continue
		}
		st.Kind = undo.KindFile
		st.Mode = uint32(info.Mode().Perm())
		data, err := os.ReadFile(abs)
		if err != nil {
			st.Present = false
		} else if len(data) > undo.MaxFileBytes {
			st.Skipped = true
		} else {
			st.Content = data
		}
		states = append(states, st)
	}
	return states
}
