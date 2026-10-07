package apps

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/GizClaw/flowcraft/core/utils"

	ocsessions "github.com/GizClaw/opencraft/internal/capabilities/sessions"
	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
	"github.com/GizClaw/opencraft/internal/foundation/utils/semver"
)

// The application manifest: <content>/app.yaml. It is the only file an
// application must carry, and the only place its identity lives — id,
// name, version, the ordered deployment layers, and what the host should
// know before assembling any of them.
//
// The file, not a database, is the source of truth. That is what makes
// "open a directory and reassemble" (the development loop) the same
// operation as an install, and what makes an application portable: a
// copied content root is a complete application.
const (
	// ManifestFile is the file name a manifest must have. A deployment
	// layer can carry any name; the manifest is found, same as
	// plugin.json for a plugin.
	ManifestFile = "app.yaml"
	// manifestMarker is the value of the top-level `app:` key. The
	// marker is what tells a manifest apart from a deployment layer
	// (whose top-level key is `version:`) in a directory that holds
	// both, so a file named app.yaml without it is a layer, not a
	// manifest.
	manifestMarker = "v1"

	// DefaultAgent is the agent an application runs when its manifest
	// names none: the reserved slot the contract layer declares, which
	// is also the only agent v1 assembles.
	DefaultAgent = "app"

	// Bounds. A manifest is a short document naming files; anything
	// past these numbers is a mistake or an attack, and either way the
	// host has to refuse it before it copies anything.
	maxManifestBytes    = 1 << 20
	maxNameChars        = 128
	maxDescriptionChars = 1024
	maxIconChars        = 64
	maxLayerCount       = 64
	maxPathLen          = 256
	maxDefaultsChars    = 128
)

// UI is the optional frontend an application ships inside its content
// root: one self-contained ES module that registers its views, and an
// optional stylesheet the host injects next to it (the app platform
// plan, §2.11). Both paths are relative to the content root and must
// stay inside it.
type UI struct {
	// Entry is the module the host imports, e.g. "ui/dist/index.js".
	Entry string `json:"entry"`
	// Style is the stylesheet the host injects as a scoped <style>.
	Style string `json:"style,omitempty"`
}

// Defaults are the per-application run defaults a new session starts
// with. Both fields are model/level names the user can override in the
// page; an empty value means "whatever the host's own default is".
//
// The host applies them where a turn starts rather than writing them
// into a document, which is what makes them reach every way an
// application's session gets started: the built-in chat surface, the
// application's own frontend bundle, a script that starts a turn of its
// own. A turn runs on what its caller named, else on what the
// conversation already carries, else on these — and because a session
// keeps the values it started on, a later change of the package's mind
// is not retroactive.
type Defaults struct {
	// Model is a deployment hint, "<deployment-id>/<model-name>", the
	// same string the page's picker sends.
	Model string `json:"model,omitempty"`
	// ThinkLevel is a reasoning level the session store accepts
	// (validateDefaults refuses one it would not, because every new
	// session would then fail on the way to persisting it).
	ThinkLevel string `json:"think_level,omitempty"`
}

// Manifest is a parsed app.yaml.
type Manifest struct {
	// App is the manifest marker (manifestMarker). A file without it is
	// not a manifest, and ParseManifest refuses it rather than
	// guessing.
	App string `json:"app"`
	// ID is the application id: the install directory name, the last
	// segment of the state root, and the usage attribution key.
	ID string `json:"id"`
	// Name is the display name the application list shows.
	Name string `json:"name"`
	// Version is the content version (semver), the thing an update
	// compares.
	Version string `json:"version"`
	// MinHostVersion is the oldest host the application runs on. An
	// empty value means "any"; a value above the running host refuses
	// the install (and the update) with the two versions named.
	MinHostVersion string `json:"minHostVersion,omitempty"`
	// Description is one line of text for the application card.
	Description string `json:"description,omitempty"`
	// Agent names the entry agent in the merged deployment document.
	// Empty means DefaultAgent.
	Agent string `json:"agent,omitempty"`
	// Icon is either a glyph/emoji (shown as the card's icon) or a path
	// to an image inside the content root. See iconIsPath.
	Icon string `json:"icon,omitempty"`
	// Layers are the application's deployment layers, in ascending
	// priority order, relative to the content root.
	Layers []string `json:"layers"`
	// UI is the optional frontend bundle.
	UI *UI `json:"ui,omitempty"`
	// Defaults are the per-application run defaults.
	Defaults *Defaults `json:"defaults,omitempty"`
	// Permissions is reserved (the app platform plan, §2.3/P3). It is
	// parsed so a declared list is refused by name instead of being
	// silently ignored: an author who writes permissions today would
	// otherwise believe they are enforced.
	Permissions []string `json:"permissions,omitempty"`
	// Generated marks a manifest the host wrote (an import of a
	// directory that had none). The host keeps the fields a user edited
	// when it rewrites one.
	Generated bool `json:"generated,omitempty"`
}

// ParseManifest decodes and validates one manifest. It checks every
// field a later step would otherwise have to re-check, and nothing that
// needs the content root — whether the layer files, the icon and the
// frontend bundle exist, and whether the layers themselves pass the
// policy, is the preflight's business (Validate), because that is the
// part that has to run again over an installed application whose files
// changed under it.
func ParseManifest(raw []byte) (*Manifest, error) {
	if len(raw) > maxManifestBytes {
		return nil, fmt.Errorf("apps: manifest exceeds %d bytes", maxManifestBytes)
	}
	m, err := utils.Decode[Manifest](raw)
	if err != nil {
		return nil, fmt.Errorf("apps: decode manifest: %w", err)
	}
	if strings.TrimSpace(m.App) != manifestMarker {
		return nil, fmt.Errorf(
			"apps: %s: not an application manifest (want app: %s)",
			ManifestFile, manifestMarker)
	}
	if err := validateManifest(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

// validateManifest checks the fields ParseManifest just decoded. It is
// separate because the store re-runs it over a manifest it read from
// disk, and because the wizard validates a draft manifest that has no
// file behind it yet.
func validateManifest(m *Manifest) error {
	if !config.ValidAppID(m.ID) {
		return fmt.Errorf(
			"apps: id %q is not an application id (want %s)",
			m.ID, `[a-z0-9][a-z0-9._-]{0,63}`)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("apps: %s: name is required", ManifestFile)
	}
	if len([]rune(m.Name)) > maxNameChars {
		return fmt.Errorf(
			"apps: %s: name exceeds %d characters", ManifestFile, maxNameChars)
	}
	if err := semver.Valid(m.Version); err != nil {
		return fmt.Errorf("apps: %s: %w", ManifestFile, err)
	}
	if m.MinHostVersion != "" {
		if err := semver.Valid(m.MinHostVersion); err != nil {
			return fmt.Errorf("apps: %s: minHostVersion: %w", ManifestFile, err)
		}
	}
	if len([]rune(m.Description)) > maxDescriptionChars {
		return fmt.Errorf(
			"apps: %s: description exceeds %d characters",
			ManifestFile, maxDescriptionChars)
	}
	if len([]rune(m.Icon)) > maxIconChars {
		return fmt.Errorf(
			"apps: %s: icon exceeds %d characters", ManifestFile, maxIconChars)
	}
	if m.Agent == "" {
		m.Agent = DefaultAgent
	}
	if !config.ValidAppID(m.Agent) {
		return fmt.Errorf(
			"apps: %s: agent %q is not an agent name (want %s)",
			ManifestFile, m.Agent, `[a-z0-9][a-z0-9._-]{0,63}`)
	}
	if err := validateLayers(m.Layers); err != nil {
		return err
	}
	if m.UI != nil {
		if err := validateUIPaths(*m.UI); err != nil {
			return err
		}
	}
	if m.Defaults != nil {
		trimDefaults(m.Defaults)
		if err := validateDefaults(m.Defaults); err != nil {
			return err
		}
	}
	if len(m.Permissions) > 0 {
		return fmt.Errorf(
			"apps: %s: permissions are not available yet (declared: %s)",
			ManifestFile, strings.Join(m.Permissions, ", "))
	}
	return nil
}

// trimDefaults strips the surrounding space off the run defaults in
// place. Both values are names — a router hint and a reasoning level —
// that the host's readers compare as written, and YAML gives an author
// no way to see a trailing space, so a padded value must mean the value
// it looks like rather than a lookup that misses.
func trimDefaults(d *Defaults) {
	d.Model = strings.TrimSpace(d.Model)
	d.ThinkLevel = strings.TrimSpace(d.ThinkLevel)
}

// validateDefaults checks the run defaults a manifest declares: short
// enough to be a name, and — for the reasoning level — a value the
// session store will accept when the host applies it to a new session.
//
// The check is here rather than at the point of use because the host has
// no way to report it that helps: an application whose manifest names a
// level the store refuses would fail every new session it starts, and
// the person who can fix it is the one who wrote (or can rewrite) the
// package. The vocabulary is the store's own, so a level it gains or
// drops moves here with it.
func validateDefaults(d *Defaults) error {
	for name, value := range map[string]string{
		"model":       d.Model,
		"think_level": d.ThinkLevel,
	} {
		if len(value) > maxDefaultsChars {
			return fmt.Errorf(
				"apps: %s: defaults.%s exceeds %d characters",
				ManifestFile, name, maxDefaultsChars)
		}
	}
	if d.ThinkLevel != "" && !ocsessions.ThinkLevel(d.ThinkLevel).Valid() {
		return fmt.Errorf(
			"apps: %s: defaults.think_level %q is not a reasoning level "+
				"(minimal, low, medium, high, xhigh)",
			ManifestFile, d.ThinkLevel)
	}
	return nil
}

// validateLayers checks the layer list as a set of names: non-empty,
// relative, inside the content root, unique, and not the manifest
// itself. Whether they exist and parse is the preflight's part.
func validateLayers(layers []string) error {
	if len(layers) == 0 {
		return fmt.Errorf("apps: %s: layers must name at least one file", ManifestFile)
	}
	if len(layers) > maxLayerCount {
		return fmt.Errorf(
			"apps: %s: %d layers exceeds the limit of %d",
			ManifestFile, len(layers), maxLayerCount)
	}
	seen := make(map[string]bool, len(layers))
	for i, name := range layers {
		entry := fmt.Sprintf("%s: layers[%d]", ManifestFile, i)
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("apps: %s: layer name is empty", entry)
		}
		if len(name) > maxPathLen {
			return fmt.Errorf("apps: %s: layer name exceeds %d bytes", entry, maxPathLen)
		}
		if !pathsafe.RelRef(name) {
			return fmt.Errorf(
				"apps: %s: layer %q must be a relative path inside the content root",
				entry, name)
		}
		if strings.HasPrefix(filepath.Base(name), ".") {
			// The installer skips dotfiles the way the plugin copier
			// does, so a layer named ".hidden.yaml" would be "found" at
			// validation time and missing after the copy.
			return fmt.Errorf(
				"apps: %s: layer %q is a dotfile; the installer skips dotfiles",
				entry, name)
		}
		if filepath.Clean(name) == ManifestFile {
			return fmt.Errorf(
				"apps: %s: layer %q is the manifest itself", entry, name)
		}
		if seen[name] {
			return fmt.Errorf("apps: %s: layer %q is listed twice", entry, name)
		}
		seen[name] = true
	}
	return nil
}

// validateUIPaths checks the two bundle paths as names, the way layers
// are checked; existence is the preflight's part.
func validateUIPaths(ui UI) error {
	entry := strings.TrimSpace(ui.Entry)
	if entry == "" {
		return fmt.Errorf("apps: %s: ui.entry is required when ui is set", ManifestFile)
	}
	if len(entry) > maxPathLen || !pathsafe.RelRef(entry) {
		return fmt.Errorf(
			"apps: %s: ui.entry %q must be a relative path inside the content root",
			ManifestFile, entry)
	}
	switch filepath.Ext(entry) {
	case ".js", ".mjs":
	default:
		return fmt.Errorf(
			"apps: %s: ui.entry %q must be an ES module (.js or .mjs)",
			ManifestFile, entry)
	}
	if ui.Style != "" {
		if len(ui.Style) > maxPathLen || !pathsafe.RelRef(ui.Style) {
			return fmt.Errorf(
				"apps: %s: ui.style %q must be a relative path inside the content root",
				ManifestFile, ui.Style)
		}
		if filepath.Clean(ui.Style) == filepath.Clean(entry) {
			return fmt.Errorf(
				"apps: %s: ui.style and ui.entry are the same file (%s)",
				ManifestFile, entry)
		}
	}
	return nil
}

// imageExts are the icon path extensions the host renders.
var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".svg": true,
	".webp": true, ".gif": true, ".avif": true,
}

// iconIsPath reports whether a manifest icon names a file in the
// content root rather than being the glyph itself. A path is a relative
// reference, so it either carries a directory separator or an image
// extension; a glyph ("🐺", "W") has neither. The distinction matters
// because only a path has to exist, and only a path can escape the
// content root.
func iconIsPath(icon string) bool {
	if icon == "" {
		return false
	}
	if strings.ContainsRune(icon, '/') || strings.ContainsRune(icon, filepath.Separator) {
		return true
	}
	return imageExts[strings.ToLower(filepath.Ext(icon))]
}
