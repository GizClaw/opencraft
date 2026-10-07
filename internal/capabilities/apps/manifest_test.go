package apps

import (
	"fmt"
	"strings"
	"testing"
)

// The manifest is the one file an application cannot do without, and the
// first thing the install reads: every check below happens before a
// single byte is copied.

// minimalManifest is the smallest manifest ParseManifest accepts: the
// fields it carries are the required ones, the rest have documented
// defaults.
const minimalManifest = `app: v1
id: hello
name: Hello
version: 0.1.0
layers:
  - layer.yaml
`

// without removes every line starting with prefix from raw.
func without(raw, prefix string) string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.HasPrefix(line, prefix) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n"
}

// manifestRefusal parses one manifest and returns the refusal, failing
// the test when it was accepted.
func manifestRefusal(t *testing.T, raw string) string {
	t.Helper()
	m, err := ParseManifest([]byte(raw))
	if err == nil {
		t.Fatalf("the manifest was accepted: %+v", m)
	}
	return err.Error()
}

// TestParseManifestReadsEveryField pins the decode side: a manifest that
// declares everything, with the fields landing where the store reads
// them and the layer order preserved.
func TestParseManifestReadsEveryField(t *testing.T) {
	m, err := ParseManifest([]byte(`app: v1
id: werewolf
name: Werewolf
description: a social deduction game
version: 1.2.3-rc.1
minHostVersion: 0.1.0
icon: ui/icon.png
layers:
  - base.yaml
  - game.yaml
ui:
  entry: ui/dist/index.js
  style: ui/dist/index.css
defaults:
  model: test-model
  think_level: high
generated: true
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.ID != "werewolf" || m.Name != "Werewolf" || m.Version != "1.2.3-rc.1" {
		t.Errorf("identity fields = %q/%q/%q", m.ID, m.Name, m.Version)
	}
	if m.MinHostVersion != "0.1.0" || m.Description != "a social deduction game" {
		t.Errorf("gate/description = %q/%q", m.MinHostVersion, m.Description)
	}
	if m.Icon != "ui/icon.png" || !m.Generated {
		t.Errorf("icon/generated = %q/%v", m.Icon, m.Generated)
	}
	// The agent defaults to the reserved slot; the layers keep the
	// manifest's order, which is their priority order.
	if m.Agent != DefaultAgent {
		t.Errorf("agent = %q, want %q", m.Agent, DefaultAgent)
	}
	if len(m.Layers) != 2 || m.Layers[0] != "base.yaml" || m.Layers[1] != "game.yaml" {
		t.Errorf("layers = %v, want the declared order", m.Layers)
	}
	if m.UI == nil || m.UI.Entry != "ui/dist/index.js" || m.UI.Style != "ui/dist/index.css" {
		t.Errorf("ui = %+v", m.UI)
	}
	if m.Defaults == nil || m.Defaults.Model != "test-model" || m.Defaults.ThinkLevel != "high" {
		t.Errorf("defaults = %+v", m.Defaults)
	}
}

// TestParseManifestTrimsTheRunDefaults: both default values are names a
// router and a session store compare as written, and YAML writes them
// with surrounding space only when the author quotes them — which is the
// shape that would otherwise miss the deployment or fail every session
// the package starts.
func TestParseManifestTrimsTheRunDefaults(t *testing.T) {
	m, err := ParseManifest([]byte(minimalManifest +
		"defaults:\n  model: \" openai-1/fake-model \"\n  think_level: \" high \"\n"))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Defaults == nil ||
		m.Defaults.Model != "openai-1/fake-model" || m.Defaults.ThinkLevel != "high" {
		t.Errorf("defaults = %+v, want the values without their surrounding space", m.Defaults)
	}
}

// TestParseManifestRefusesAPackageThatIsNotAManifest: the `app:` marker
// is what tells a manifest from a deployment layer in a directory that
// holds both, so a file without it is refused instead of half-read.
func TestParseManifestRefusesAPackageThatIsNotAManifest(t *testing.T) {
	err := manifestRefusal(t, without(minimalManifest, "app:"))
	if !strings.Contains(err, "not an application manifest (want app: v1)") {
		t.Errorf("refusal %q does not name the marker", err)
	}
}

// TestParseManifestRefusesOneFieldAtATime is the table of the checks a
// manifest can fail: each case changes exactly one thing, so the refusal
// it reads is the one the author has to fix.
func TestParseManifestRefusesOneFieldAtATime(t *testing.T) {
	longest := strings.Repeat("x", maxNameChars+1)
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "id with an uppercase letter",
			raw:  strings.Replace(minimalManifest, "id: hello", "id: Hello", 1),
			want: `id "Hello" is not an application id`,
		},
		{
			name: "id that walks up",
			raw:  strings.Replace(minimalManifest, "id: hello", "id: ../hello", 1),
			want: "is not an application id",
		},
		{
			name: "no name",
			raw:  without(minimalManifest, "name:"),
			want: "name is required",
		},
		{
			name: "name too long",
			raw:  strings.Replace(minimalManifest, "Hello", longest, 1),
			want: "name exceeds 128 characters",
		},
		{
			name: "no version",
			raw:  without(minimalManifest, "version:"),
			want: `invalid version ""`,
		},
		{
			name: "version that is not semver",
			raw:  strings.Replace(minimalManifest, "0.1.0", "0.1.x", 1),
			want: `invalid version "0.1.x"`,
		},
		{
			name: "minHostVersion that is not semver",
			raw:  minimalManifest + "minHostVersion: x\n",
			want: `minHostVersion: invalid version "x"`,
		},
		{
			name: "no layers",
			raw:  without(without(minimalManifest, "layers:"), "  - layer.yaml"),
			want: "layers must name at least one file",
		},
		{
			name: "layer that walks out of the content root",
			raw:  strings.Replace(minimalManifest, "- layer.yaml", "- ../layer.yaml", 1),
			want: "must be a relative path inside the content root",
		},
		{
			name: "absolute layer",
			raw:  strings.Replace(minimalManifest, "- layer.yaml", "- /etc/layer.yaml", 1),
			want: "must be a relative path inside the content root",
		},
		{
			name: "dotfile layer",
			raw:  strings.Replace(minimalManifest, "- layer.yaml", "- .layer.yaml", 1),
			want: "is a dotfile; the installer skips dotfiles",
		},
		{
			name: "the manifest as its own layer",
			raw:  strings.Replace(minimalManifest, "- layer.yaml", "- app.yaml", 1),
			want: "is the manifest itself",
		},
		{
			name: "the same layer twice",
			raw: strings.Replace(minimalManifest, "  - layer.yaml",
				"  - layer.yaml\n  - layer.yaml", 1),
			want: "is listed twice",
		},
		{
			name: "more layers than the limit",
			raw:  manifestWithLayers(maxLayerCount + 1),
			want: "exceeds the limit of 64",
		},
		{
			name: "ui without an entry",
			raw:  minimalManifest + "ui:\n  style: ui/index.css\n",
			want: "ui.entry is required when ui is set",
		},
		{
			name: "ui entry that is not a module",
			raw:  minimalManifest + "ui:\n  entry: ui/index.txt\n",
			want: "must be an ES module (.js or .mjs)",
		},
		{
			name: "ui entry outside the content root",
			raw:  minimalManifest + "ui:\n  entry: ../index.js\n",
			want: `ui.entry "../index.js" must be a relative path inside the content root`,
		},
		{
			name: "ui style outside the content root",
			raw:  minimalManifest + "ui:\n  entry: ui/index.js\n  style: ../index.css\n",
			want: `ui.style "../index.css" must be a relative path inside the content root`,
		},
		{
			name: "ui entry and style are the same file",
			raw:  minimalManifest + "ui:\n  entry: ui/index.js\n  style: ui/index.js\n",
			want: "ui.style and ui.entry are the same file",
		},
		{
			name: "icon too long",
			raw:  minimalManifest + "icon: " + strings.Repeat("x", maxIconChars+1) + "\n",
			want: "icon exceeds 64 characters",
		},
		{
			name: "description too long",
			raw:  minimalManifest + "description: " + strings.Repeat("x", maxDescriptionChars+1) + "\n",
			want: "description exceeds 1024 characters",
		},
		{
			name: "agent that is not an agent name",
			raw:  minimalManifest + "agent: The Wolf\n",
			want: `agent "The Wolf" is not an agent name`,
		},
		{
			name: "defaults value too long",
			raw:  minimalManifest + "defaults:\n  model: " + strings.Repeat("x", maxDefaultsChars+1) + "\n",
			want: "defaults.model exceeds 128 characters",
		},
		{
			name: "defaults think level the session store would refuse",
			raw:  minimalManifest + "defaults:\n  think_level: extreme\n",
			want: "defaults.think_level \"extreme\" is not a reasoning level",
		},
		{
			name: "permissions",
			raw:  minimalManifest + "permissions:\n  - net\n",
			want: "permissions are not available yet (declared: net)",
		},
		{
			name: "unknown field",
			raw:  minimalManifest + "speed: fast\n",
			want: `unknown field "speed"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := manifestRefusal(t, tc.raw)
			if !strings.Contains(err, tc.want) {
				t.Errorf("refusal %q does not mention %q", err, tc.want)
			}
		})
	}
}

// manifestWithLayers renders a manifest listing n layers.
func manifestWithLayers(n int) string {
	var b strings.Builder
	b.WriteString(without(without(minimalManifest, "layers:"), "  - layer.yaml"))
	b.WriteString("layers:\n")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "  - layer-%d.yaml\n", i)
	}
	return b.String()
}

// TestParseManifestRefusesAnOversizeManifest: a manifest is a short
// document naming files, so the size gate is part of the format, not an
// implementation detail.
func TestParseManifestRefusesAnOversizeManifest(t *testing.T) {
	raw := minimalManifest + "# " + strings.Repeat("x", maxManifestBytes)
	err := manifestRefusal(t, raw)
	if !strings.Contains(err, "manifest exceeds 1048576 bytes") {
		t.Errorf("refusal %q does not name the limit", err)
	}
}

// TestIconIsPath pins the one field a manifest may use for two things: a
// glyph is the icon, anything path-shaped is a file that has to exist.
func TestIconIsPath(t *testing.T) {
	glyphs := []string{"", "W", "🐺", "🎲"}
	for _, icon := range glyphs {
		if iconIsPath(icon) {
			t.Errorf("iconIsPath(%q) = true, want a glyph", icon)
		}
	}
	paths := []string{"icon.png", "ICON.PNG", "images/logo", "ui/icon.svg", "icon.gif"}
	for _, icon := range paths {
		if !iconIsPath(icon) {
			t.Errorf("iconIsPath(%q) = false, want a path", icon)
		}
	}
}
