package config

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/GizClaw/flowcraft/core/media/hook"
)

// TestEmbeddedDocumentCarriesNoEmptySource pins the shipped side of the
// deploy-source rule: flowcraft parses a resource's settings subtree —
// and an agent's engine graph reference inside its settings — as a
// whole-subtree source and rejects the empty object, which fails the
// build of the whole document rather than of that one entry. An asset
// edit that leaves one behind would brick every workspace, so the
// shipped layers never carry one (see nonEmptySettings for the writer
// side of the same rule).
func TestEmbeddedDocumentCarriesNoEmptySource(t *testing.T) {
	mgr, err := Open(Options{UserDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for name, res := range view.Document.Resources {
		if isEmptySource(res.Settings) {
			t.Errorf("resource %q carries settings %s",
				name, strings.TrimSpace(string(res.Settings)))
		}
	}
	for name, def := range view.Document.Agents {
		var settings map[string]json.RawMessage
		if err := json.Unmarshal(def.Engine.Settings, &settings); err != nil {
			t.Errorf("agent %q engine settings: %v", name, err)
			continue
		}
		if isEmptySource(settings["graph"]) {
			t.Errorf("agent %q carries graph %s",
				name, strings.TrimSpace(string(settings["graph"])))
		}
	}
}

// isEmptySource reports whether raw is one of the empty spellings the
// deploy source parser rejects.
func isEmptySource(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return false
	}
	return bytes.Equal(trimmed, []byte("{}")) ||
		bytes.Equal(trimmed, []byte("null"))
}

// TestEmbeddedAssetsKeepContentOffTheStateRoot pins the two-root split
// the deploy document relies on: content and credentials (keyring,
// agents, user skills, hooks, the plugin registry) live under the app
// home, which every instance of one user shares, while the state root
// (workspaces, user.db, logs, audit, cache) belongs to one process.
//
// A ${ocraft:DATA_DIR} reference in an embedded asset would silently
// send a dev profile at the installed app's content — the exact failure
// the split exists to prevent — so the contract is "none". The resolver
// still provides the value: external plugin manifests may ask for it.
func TestEmbeddedAssetsKeepContentOffTheStateRoot(t *testing.T) {
	const forbidden = "${ocraft:DATA_DIR}"
	root := FS()
	err := fs.WalkDir(root, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path.Ext(p) != ".yaml" {
			return nil
		}
		data, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		if !strings.Contains(string(data), forbidden) {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, forbidden) {
				t.Errorf("%s:%d references %s: %s",
					p, i+1, forbidden, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded assets: %v", err)
	}
}

// TestEmbeddedAssetsResolveAppHome pins the other half of the same
// contract: the content paths must keep pointing at the app home value,
// so a rename of the resolver value cannot slip through unnoticed.
func TestEmbeddedAssetsResolveAppHome(t *testing.T) {
	root := FS()
	// opencraft.yaml: keychain dir, user skills root, hooks path. The
	// file-tool staging roots moved to ${ocraft:CACHE} instead, which is
	// state (the execd runner cache lives there too).
	want := map[string]int{
		// The skill lifecycle archive is the fourth: retiring a skill
		// snapshots it under the app home, so the snapshot survives a
		// workspace being removed.
		"assets/opencraft.yaml": 4,
		"assets/agents.yaml":    1,
		"assets/tools.yaml":     2,
	}
	for file, count := range want {
		data, err := fs.ReadFile(root, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		got := strings.Count(string(data), "${ocraft:APP_HOME}")
		if got != count {
			t.Errorf("%s: %d ${ocraft:APP_HOME} references, want %d",
				file, got, count)
		}
	}
}

// TestEmbeddedAssistantPreparesAttachmentsWithTheUpstreamHook pins the
// deployment half of the attachment path: the assistant's first prepare
// hook is flowcraft's media.attachments factory (core/media/hook), and
// the flattened lines keep the markers sessions already recorded —
// moving the hook out of internal/capabilities/media must not change
// the prompt text a turn carries. audio/video/file stay flattened: the
// hook runs before routing, so a kind passes through only where a
// deployment knows its models take it (passthrough_kinds, user layer).
func TestEmbeddedAssistantPreparesAttachmentsWithTheUpstreamHook(t *testing.T) {
	mgr, err := Open(Options{UserDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	view, err := mgr.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, h := range view.Document.Agents["assistant"].Prepare {
		if h.Type != hook.AttachmentsType {
			continue
		}
		found++
		var settings hook.Settings
		if err := json.Unmarshal(h.Settings, &settings); err != nil {
			t.Fatalf("decode %s settings: %v", h.Type, err)
		}
		if settings.WorkDir != "${ocraft:WORKDIR}" {
			t.Errorf("work_dir = %q, want the workspace root", settings.WorkDir)
		}
		markers := map[string]string{
			"image_marker": settings.ImageMarker,
			"audio_marker": settings.AudioMarker,
			"video_marker": settings.VideoMarker,
			"file_marker":  settings.FileMarker,
		}
		want := map[string]string{
			"image_marker": "[图片文件] ",
			"audio_marker": "[音频文件] ",
			"video_marker": "[视频文件] ",
			"file_marker":  "[一般文件] ",
		}
		for name, got := range markers {
			if got != want[name] {
				t.Errorf("%s = %q, want %q", name, got, want[name])
			}
		}
		if len(settings.PassthroughKinds) != 0 {
			t.Errorf("passthrough_kinds = %v, want every non-image kind "+
				"flattened by default", settings.PassthroughKinds)
		}
	}
	if found != 1 {
		t.Fatalf("%s hooks in the assistant's prepare list = %d, want 1",
			hook.AttachmentsType, found)
	}
}
