package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"

	"github.com/GizClaw/flowcraft/core/telemetry"

	"github.com/GizClaw/opencraft/internal/foundation/config"
	"github.com/GizClaw/opencraft/internal/foundation/platform/maxpath"
	"github.com/GizClaw/opencraft/internal/foundation/utils/pathsafe"
	"github.com/GizClaw/opencraft/internal/foundation/utils/semver"
	"github.com/GizClaw/opencraft/internal/foundation/version"
)

// The application registry: one directory per installed application
// under the user content root, an optional read-only built-in root
// beside the executable, and the enable/disable choices.
//
// Layout (see foundation/config for the state half):
//
//	<appHome>/apps/                 store root (Options.Root)
//	  state.json                    enable/disable choices
//	  <id>/content/                 the installed content root:
//	                                app.yaml, the deployment layers,
//	                                the graphs/scripts/prompts they
//	                                reference, ui/
//	  <id>/workspace/ …             the application's state root, which
//	                                is <dataDir>/apps/<id> — the same
//	                                directory in the single-root layout,
//	                                a different tree when a profile
//	                                separates data from content
//
// The two halves are addressed separately on purpose. Installing,
// updating or uninstalling touches `<id>/content` and nothing else, so a
// reinstall cannot overwrite the sessions, the private workspace or the
// approvals of an application that is already running; `purge` is the
// one operation that reaches the state root, and it says so in its name.

// ErrNotInstalled reports an id no content root holds.
var ErrNotInstalled = errors.New("apps: no such application")

// stateFile records explicit enable/disable choices, keyed by id. An
// application without an entry is enabled: "installed" is the intent, and
// a user who disables one writes the entry that says otherwise.
const stateFile = "state.json"

// Options configures a Store.
type Options struct {
	// Root is the writable content root: <appHome>/apps. Required.
	Root string
	// DataDir is the state root parent: an application's state is
	// <DataDir>/apps/<id> (foundation/config.AppLayout). Empty means
	// the single-root layout, where the state root is <Root>/<id>
	// itself.
	DataDir string
	// Builtin is the optional read-only root of app-bundled
	// applications (see BuiltinAppRoot). Empty disables built-ins.
	Builtin string
	// HostVersion is the running host version a manifest's
	// minHostVersion is checked against (foundation/version).
	// Empty skips the check.
	HostVersion string
}

// Store is the application registry.
type Store struct {
	root        string
	dataDir     string
	builtin     string
	hostVersion string
	// pathLimit is the longest landing path this host's file APIs
	// accept, or 0 when it caps none (platform/maxpath, landing.go). It
	// is a field so that the preflight and every install read one
	// answer, and so that the check can be exercised on any platform.
	pathLimit int
}

// NewStore returns the registry described by o. It creates nothing: an
// install creates the root it needs, a read on a machine that never
// installed anything is a read of an empty registry.
func NewStore(o Options) *Store {
	return &Store{
		root:        o.Root,
		dataDir:     o.DataDir,
		builtin:     o.Builtin,
		hostVersion: o.HostVersion,
		pathLimit:   maxpath.HostLimit(),
	}
}

// NewRegistry builds the registry one launch serves: content roots under
// <appHome>/apps, state roots under <dataDir>/apps (the same tree in the
// single-root layout), the read-only built-ins beside the running
// executable, and this build's version as the one a manifest's
// minHostVersion is checked against. The launch paths are the ones
// config.ResolveLaunch resolved, so a composition root that has them
// builds one registry and hands that instance to everything that reads
// it — the pool that assembles an application and the page that installs
// one — instead of each half deriving its own roots.
func NewRegistry(appHome, dataDir string) (*Store, error) {
	root, err := config.AppsContentDir(appHome)
	if err != nil {
		return nil, err
	}
	return NewStore(Options{
		Root:        root,
		DataDir:     dataDir,
		Builtin:     BuiltinAppRoot(),
		HostVersion: version.ServiceVersion,
	}), nil
}

// BuiltinAppRoot returns the read-only, app-bundled application
// directory next to the running executable, or "" when absent (dev runs,
// platforms without a bundled layout). The layout is the plugin one:
// macOS apps ship under Contents/Resources/apps, other platforms keep an
// apps/ directory next to the binary.
func BuiltinAppRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	var root string
	if goruntime.GOOS == "darwin" {
		root = filepath.Join(exeDir, "..", "Resources", "apps")
	} else {
		root = filepath.Join(exeDir, "apps")
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

// App is one installed application as the registry and the host consume
// it: what an assembly needs (the content root and the layers, in
// order), what the page shows, and which agent runs.
type App struct {
	// ID is the installed application id.
	ID string
	// Name is the display name.
	Name string
	// Version is the content version.
	Version string
	// Description is the one-line description the card shows.
	Description string
	// Icon is the glyph or the content-root-relative image path.
	Icon string
	// Agent is the entry agent in the merged deployment document.
	Agent string
	// ContentDir is the content root: the layers and every {file:}
	// reference inside them resolve against it.
	ContentDir string
	// Layers are the deployment layers, in ascending priority order,
	// relative to ContentDir.
	Layers []string
	// UI is the optional frontend bundle, relative to ContentDir.
	UI UI
	// Enabled reports the registry's enable/disable choice.
	Enabled bool
	// Builtin reports a read-only, app-bundled application.
	Builtin bool
}

// Summary is the frontend-facing view of one application: what the list
// and the card need, without the paths. A broken install still yields a
// summary — with Error set — because the only way to repair one is to
// see it in the list.
type Summary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Agent       string `json:"agent,omitempty"`
	Enabled     bool   `json:"enabled"`
	Builtin     bool   `json:"builtin,omitempty"`
	// HasUI reports whether the application ships its own frontend, so
	// the page knows whether it has views beyond the built-in
	// conversation.
	HasUI bool `json:"hasUi,omitempty"`
	// CanRollback reports whether the last update left the version it
	// replaced behind (update.go). It stays true through a failed
	// assembly — the snapshot is on disk either way — and goes false
	// once a rollback consumes it.
	CanRollback bool `json:"canRollback,omitempty"`
	// Error is the reason a manifest could not be read: the install is
	// there, it is just unusable until the file is fixed.
	Error string `json:"error,omitempty"`
}

// List returns every installed application, by id, the user root
// shadowing the built-in one. An entry that is only a state root (an
// application that was uninstalled with its data kept) is not an
// installed application and does not appear.
func (s *Store) List() ([]Summary, error) {
	state, err := s.readState()
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, 8)
	seen := make(map[string]bool)
	for _, root := range []struct {
		dir     string
		builtin bool
	}{{s.root, false}, {s.builtin, true}} {
		if root.dir == "" {
			continue
		}
		entries, err := os.ReadDir(root.dir)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("apps: read %s: %w", root.dir, err)
		}
		for _, entry := range entries {
			id := entry.Name()
			if !entry.IsDir() || strings.HasPrefix(id, ".") || !config.ValidAppID(id) {
				continue
			}
			if seen[id] {
				continue
			}
			content := filepath.Join(root.dir, id, "content")
			if info, err := os.Stat(content); err != nil || !info.IsDir() {
				continue
			}
			seen[id] = true
			summary := Summary{ID: id, Enabled: s.enabled(state, id), Builtin: root.builtin}
			// A snapshot is a user-root thing: a built-in application's
			// content comes from the read-only bundle and is never
			// updated in place.
			summary.CanRollback = !root.builtin && s.canRollback(id)
			m, err := s.readManifest(content, id)
			if err != nil {
				summary.Error = err.Error()
				out = append(out, summary)
				continue
			}
			summary.Name = m.Name
			summary.Description = m.Description
			summary.Version = m.Version
			summary.Icon = m.Icon
			summary.Agent = m.Agent
			summary.HasUI = m.UI != nil && m.UI.Entry != ""
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Get returns one installed application: the content root, the manifest
// facts and the enable state. It is the read path an assembly takes, so
// it resolves the same user-then-built-in order List reports and does
// not run the preflight — that is Validate's job, and it runs where the
// application is about to be used.
func (s *Store) Get(id string) (App, error) {
	content, builtin, err := s.contentDir(id)
	if err != nil {
		return App{}, err
	}
	m, err := s.readManifest(content, id)
	if err != nil {
		return App{}, err
	}
	state, err := s.readState()
	if err != nil {
		return App{}, err
	}
	app := App{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Description: m.Description,
		Icon:        m.Icon,
		Agent:       m.Agent,
		ContentDir:  content,
		Layers:      append([]string(nil), m.Layers...),
		Enabled:     s.enabled(state, id),
		Builtin:     builtin,
	}
	if m.UI != nil {
		app.UI = *m.UI
	}
	return app, nil
}

// Manifest returns the parsed manifest of one installed application.
func (s *Store) Manifest(id string) (*Manifest, error) {
	content, _, err := s.contentDir(id)
	if err != nil {
		return nil, err
	}
	return s.readManifest(content, id)
}

// SetEnabled turns one installed application on or off. A call that
// changes nothing writes nothing.
func (s *Store) SetEnabled(id string, enabled bool) error {
	if _, _, err := s.contentDir(id); err != nil {
		return err
	}
	return s.setEnabledState(id, enabled)
}

// setEnabledState records one enable/disable choice.
func (s *Store) setEnabledState(id string, enabled bool) error {
	state, err := s.readState()
	if err != nil {
		return err
	}
	// No state entry means enabled, so an explicit enable of an
	// application that was never disabled is the same no-op as a
	// repeat call.
	current, ok := state[id]
	if !ok {
		current = true
	}
	if current == enabled {
		return nil
	}
	state[id] = enabled
	return s.writeState(state)
}

// Uninstall removes one installed application. The content is always
// removed; purge also removes the state root — the sessions, the private
// workspace and everything else the application wrote — which is why it
// is the caller's explicit decision (the page asks before it passes
// true).
//
// A built-in application cannot be uninstalled: its content lives in a
// read-only bundle, so the only honest answer is to disable it.
func (s *Store) Uninstall(id string, purge bool) error {
	content, builtin, err := s.contentDir(id)
	if err != nil {
		return err
	}
	if builtin {
		return fmt.Errorf(
			"apps: %q is built in and cannot be uninstalled; disable it instead", id)
	}
	appDir := filepath.Dir(content)
	if err := os.RemoveAll(content); err != nil {
		return fmt.Errorf("apps: remove %q: %w", id, err)
	}
	if purge {
		stateRoot, err := s.stateRoot(id)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(stateRoot); err != nil {
			return fmt.Errorf("apps: remove %q state: %w", id, err)
		}
	}
	// The snapshot is the version of the content that just went away, so
	// it goes with it: unloading an application must not leave a version
	// of it on disk that nothing can ever restore.
	telemetry.WarnErr(context.Background(),
		"apps: remove rollback snapshot failed",
		os.RemoveAll(s.backupDir(id)))
	// The application directory itself goes only when nothing is left in
	// it: with a state root of its own, the sessions and the private
	// workspace stay (the state root, not the content root, is where an
	// application's data lives).
	removeIfEmpty(appDir)
	state, err := s.readState()
	if err != nil {
		return err
	}
	if _, ok := state[id]; ok {
		delete(state, id)
		return s.writeState(state)
	}
	return nil
}

// appFromManifest builds the consume value of a manifest for one content
// root, without reading the enable state: the install path validates
// what it is about to copy, and a staged directory is not installed yet.
func (s *Store) appFromManifest(m *Manifest, content string, enabled bool) App {
	app := App{
		ID:          m.ID,
		Name:        m.Name,
		Version:     m.Version,
		Description: m.Description,
		Icon:        m.Icon,
		Agent:       m.Agent,
		ContentDir:  content,
		Layers:      append([]string(nil), m.Layers...),
		Enabled:     enabled,
	}
	if m.UI != nil {
		app.UI = *m.UI
	}
	return app
}

// checkSource refuses a source that cannot be copied into the registry:
// not a directory, or one that lives inside the registry itself (an
// install would recurse into its own staging area).
func (s *Store) checkSource(src string) error {
	if strings.TrimSpace(src) == "" {
		return errors.New("apps: source is required")
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("apps: source: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("apps: source %q is not a directory", src)
	}
	return s.checkOutsideRoot(src)
}

// checkOutsideRoot refuses a source inside the content root. An install
// from there would copy the registry into its own staging area, and the
// preflight reads a candidate out of a tree that may not be the
// registry's either way.
func (s *Store) checkOutsideRoot(src string) error {
	if pathsafe.Within(s.root, src) {
		return fmt.Errorf(
			"apps: source %q is inside the application root %q", src, s.root)
	}
	return nil
}

// checkHostVersion enforces manifest.minHostVersion against the running
// host when both are known.
func (s *Store) checkHostVersion(m *Manifest) error {
	if m.MinHostVersion == "" || s.hostVersion == "" {
		return nil
	}
	cmp, err := semver.Compare(m.MinHostVersion, s.hostVersion)
	if err != nil {
		return fmt.Errorf("apps: %s: %w", ManifestFile, err)
	}
	if cmp > 0 {
		return fmt.Errorf(
			"apps: %q requires host %s, running %s",
			m.ID, m.MinHostVersion, s.hostVersion)
	}
	return nil
}

// contentDir resolves the content-root directory holding an installed
// application: the user root wins, otherwise the built-in one. builtin
// reports whether it lives in the read-only bundle.
func (s *Store) contentDir(id string) (string, bool, error) {
	if !config.ValidAppID(id) {
		return "", false, fmt.Errorf("apps: invalid id %q", id)
	}
	if strings.TrimSpace(s.root) == "" {
		return "", false, errors.New("apps: content root is not configured")
	}
	user := s.landingRoot(id)
	if info, err := os.Stat(user); err == nil && info.IsDir() {
		return user, false, nil
	}
	if s.builtin != "" {
		builtin := filepath.Join(s.builtin, id, "content")
		if info, err := os.Stat(builtin); err == nil && info.IsDir() {
			return builtin, true, nil
		}
	}
	return "", false, fmt.Errorf("%w: %q", ErrNotInstalled, id)
}

// stateRoot returns the state root of one application: the tree the
// runtime may write, and the one purge removes.
func (s *Store) stateRoot(id string) (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		// The single-root layout: content and state share <Root>/<id>.
		return filepath.Join(s.root, id), nil
	}
	return config.AppStateRoot(s.dataDir, id)
}

// StateRoot returns the state root of one installed application: the
// tree its runtime may write — sessions, the private workspace, cache,
// audit — and the one `purge` removes. It creates nothing and refuses an
// id no content root holds, so a caller that names a state root has an
// installed application behind it.
func (s *Store) StateRoot(id string) (string, error) {
	if _, _, err := s.contentDir(id); err != nil {
		return "", err
	}
	return s.stateRoot(id)
}

// ContentRoot returns the content-root directory of one installed
// application without reading its manifest: the tree an install, an
// update and an uninstall move, and the one an author edits. A manifest
// that no longer parses leaves this answer standing — unlike Get, which
// reads it — which is what a development loop needs: the file that has
// to be repaired is inside the directory this returns, and the watcher
// over it (watch.go) keeps watching a root whose manifest is broken.
func (s *Store) ContentRoot(id string) (string, error) {
	content, _, err := s.contentDir(id)
	return content, err
}

// WorkDir returns the private workspace of one installed application:
// the one directory its runtime reads and writes, and the tree the page
// browses. It is named, not created — the application's own assembly is
// what ensures the state directories exist.
func (s *Store) WorkDir(id string) (string, error) {
	if _, _, err := s.contentDir(id); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.dataDir) == "" {
		// The single-root layout: the store root is the data dir, so the
		// layout's reader takes it as one (see config.AppWorkDir).
		return config.AppWorkDir(s.root, id)
	}
	return config.AppWorkDir(s.dataDir, id)
}

// readManifest reads and parses app.yaml out of one content root. id is
// the expected id ("" while installing, where the manifest is what names
// it), so a manifest that was edited into a different identity is
// refused rather than silently re-homed.
func (s *Store) readManifest(content, id string) (*Manifest, error) {
	raw, err := os.ReadFile(filepath.Join(content, ManifestFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("apps: %s is missing", ManifestFile)
		}
		return nil, fmt.Errorf("apps: read %s: %w", ManifestFile, err)
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	if id != "" && m.ID != id {
		return nil, fmt.Errorf(
			"apps: %s declares id %q but is installed as %q",
			ManifestFile, m.ID, id)
	}
	return m, nil
}

// readState reads the enable/disable choices.
func (s *Store) readState() (map[string]bool, error) {
	state := map[string]bool{}
	raw, err := os.ReadFile(filepath.Join(s.root, stateFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return state, nil
		}
		return nil, fmt.Errorf("apps: read state: %w", err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("apps: decode state: %w", err)
	}
	return state, nil
}

// writeState writes the enable/disable choices.
func (s *Store) writeState(state map[string]bool) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return fmt.Errorf("apps: create content root: %w", err)
	}
	path := filepath.Join(s.root, stateFile)
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("apps: write state: %w", err)
	}
	return nil
}

// enabled reports the choice for one id: no entry means enabled.
func (s *Store) enabled(state map[string]bool, id string) bool {
	if value, ok := state[id]; ok {
		return value
	}
	return true
}

// summaryFromManifest renders the list view of one installed
// application.
func summaryFromManifest(m *Manifest, enabled bool) Summary {
	return Summary{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Version:     m.Version,
		Icon:        m.Icon,
		Agent:       m.Agent,
		Enabled:     enabled,
		HasUI:       m.UI != nil && m.UI.Entry != "",
	}
}

// copyTree copies an application source into the registry: directories
// 0700, files 0600, dotfiles skipped (".git", ".DS_Store", editor
// leftovers — and the manifest validation refuses a layer that would be
// skipped here, so the copy and the validation agree about what the
// content is).
//
// A symbolic link is refused rather than followed: the content root the
// host reads at runtime is the copy, and a link that resolves to
// somewhere else would make "inside the content root" mean two different
// things before and after the install.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o700)
		}
		if strings.HasPrefix(filepath.Base(rel), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symbolic link", rel)
		}
		target := filepath.Join(dst, rel)
		switch {
		case info.IsDir():
			return os.MkdirAll(target, 0o700)
		case !info.Mode().IsRegular():
			return fmt.Errorf("%s is not a regular file (%s)", rel, info.Mode().Type())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

// removeIfEmpty removes a directory only when nothing is left in it.
// Failures are not reported: the caller removed what it came for, and a
// directory that still holds state is the normal outcome.
func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) > 0 {
		return
	}
	telemetry.WarnErr(context.Background(),
		"apps: remove empty application dir failed", os.Remove(dir))
}
