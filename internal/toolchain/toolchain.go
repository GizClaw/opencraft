// Package toolchain manages OpenCraft's bundled language/tool runtimes
// (Python, Node, uv/uvx). Release apps ship compressed runtime archives
// next to the binary; this package extracts a family into the user
// cache only when an external tool is missing. Go is intentionally not
// bundled and stays external-only when present on PATH.
package toolchain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ResourceKind is the deploy resource kind implemented by this
// package.
const ResourceKind = "opencraft.toolchain"

// ResourceImpl is the deploy impl id of the local runtime manager.
const ResourceImpl = "local"

// Preference controls how bare tool commands resolve.
type Preference string

const (
	// PreferenceExternalFirst prefers tools already on the external
	// PATH and only falls back to bundled sidecars.
	PreferenceExternalFirst Preference = "external-first"
	// PreferenceBundledFirst prefers bundled sidecars before external
	// tools.
	PreferenceBundledFirst Preference = "bundled-first"
	// PreferenceOff disables bundled runtimes entirely.
	PreferenceOff Preference = "off"
)

// NormalizePreference validates a runtime_preference value. An empty
// value maps to the external-first default.
func NormalizePreference(value string) (Preference, error) {
	switch Preference(strings.TrimSpace(value)) {
	case "":
		return PreferenceExternalFirst, nil
	case PreferenceExternalFirst, PreferenceBundledFirst, PreferenceOff:
		return Preference(value), nil
	default:
		return "", fmt.Errorf(
			"runtimes: unknown runtime_preference %q (want external-first, bundled-first or off)",
			value)
	}
}

// Settings is the deploy-document shape of the runtimes resource.
type Settings struct {
	Preference      string `json:"preference,omitempty"`
	Root            string `json:"root,omitempty"`
	ManifestPath    string `json:"manifest_path,omitempty"`
	SandboxCacheDir string `json:"sandbox_cache_dir,omitempty"`
	HostCacheDir    string `json:"host_cache_dir,omitempty"`
	CacheDir        string `json:"cache_dir,omitempty"`
}

// DefaultSettings returns the embedded defaults.
func DefaultSettings() Settings {
	return Settings{Preference: string(PreferenceExternalFirst)}
}

// Source is where one resolved tool came from.
type Source string

const (
	// SourceSystem is a tool found on the external PATH.
	SourceSystem Source = "system"
	// SourceBundled is a tool inside the app sidecar.
	SourceBundled Source = "bundled"
)

// Runtime is one resolved tool executable.
type Runtime struct {
	Tool    string
	Family  string
	Version string
	Path    string
	BinDir  string
	Root    string
	Source  Source
}

// RuntimeStatus is the diagnostics-page view of one tool.
type RuntimeStatus struct {
	Tool    string `json:"tool"`
	Family  string `json:"family"`
	Version string `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
	Path    string `json:"path,omitempty"`
	Error   string `json:"error,omitempty"`
}

// manifestFile is the on-disk runtime manifest (metadata only; binary
// archives are staged by release CI).
type manifestFile struct {
	SchemaVersion int            `json:"schema_version"`
	Python        *manifestEntry `json:"python,omitempty"`
	Go            *manifestEntry `json:"go,omitempty"`
	Node          *manifestEntry `json:"node,omitempty"`
	UV            *manifestEntry `json:"uv,omitempty"`
}

type manifestEntry struct {
	Version string            `json:"version"`
	URLs    map[string]string `json:"urls"`
	SHA256  map[string]string `json:"sha256"`
}

// entry returns the manifest entry for one runtime family.
func (m *manifestFile) entry(family string) *manifestEntry {
	switch family {
	case "python":
		return m.Python
	case "go":
		return m.Go
	case "node":
		return m.Node
	case "uv":
		return m.UV
	default:
		return nil
	}
}

// toolFamilies maps every bare tool name to its runtime family.
var toolFamilies = map[string]string{
	"python":   "python",
	"python3":  "python",
	"go":       "go",
	"gofmt":    "go",
	"node":     "node",
	"npm":      "node",
	"npx":      "node",
	"corepack": "node",
	"uv":       "uv",
	"uvx":      "uv",
}

// binRel returns the executable directory inside one runtime family
// root. Windows Python/Node archives put executables at the root;
// Go keeps bin/ and uv carries uv/uvx at the archive root.
func binRel(family string) string {
	if runtime.GOOS == "windows" && (family == "python" || family == "node") {
		return ""
	}
	switch family {
	case "uv":
		return ""
	case "go":
		return "bin"
	default:
		return "bin"
	}
}

// Options configures a Manager.
type Options struct {
	Preference      Preference
	Root            string
	ManifestPath    string
	SandboxCacheDir string
	HostCacheDir    string
	CacheDir        string
}

// Manager resolves tools against external and bundled runtimes.
type Manager struct {
	preference      Preference
	root            string
	manifestPath    string
	manifest        *manifestFile
	launcherDir     string
	sandboxCacheDir string
	hostCacheDir    string
	cacheDir        string
}

// New builds a Manager. Root, manifest and cache paths default from
// the running executable / environment when omitted.
func New(opts Options) (*Manager, error) {
	pref := opts.Preference
	if pref == "" {
		pref = PreferenceExternalFirst
	}
	root := opts.Root
	if root == "" {
		root = BundledRoot()
	}
	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = defaultCacheDir()
	}
	manifestPath := opts.ManifestPath
	if manifestPath == "" && root != "" {
		candidate := filepath.Join(root, "manifest.json")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			manifestPath = candidate
		}
	}
	var manifest *manifestFile
	if manifestPath != "" {
		var err error
		manifest, err = loadManifest(manifestPath)
		if err != nil {
			return nil, err
		}
	}
	launcherDir := ""
	if root != "" {
		candidate := filepath.Join(root, "launcher")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			launcherDir = candidate
		}
	}
	return &Manager{
		preference:      pref,
		root:            root,
		manifestPath:    manifestPath,
		manifest:        manifest,
		launcherDir:     launcherDir,
		sandboxCacheDir: opts.SandboxCacheDir,
		hostCacheDir:    opts.HostCacheDir,
		cacheDir:        cacheDir,
	}, nil
}

// Preference returns the effective resolution preference.
func (m *Manager) Preference() Preference {
	return m.preference
}

// Root returns the bundled runtime root ("" when no sidecar exists).
func (m *Manager) Root() string {
	return m.root
}

// LauncherDir returns the read-only per-tool launcher directory, or
// "" when no launcher is bundled or runtimes are disabled.
func (m *Manager) LauncherDir() string {
	if m.preference == PreferenceOff {
		return ""
	}
	return m.launcherDir
}

// BundledRoot returns the platform-specific bundled runtime root next
// to the running executable, or "" when absent.
func BundledRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exe)
	var root string
	if runtime.GOOS == "darwin" {
		root = filepath.Join(exeDir, "..", "Resources", "runtime")
	} else {
		root = filepath.Join(exeDir, "runtime")
	}
	root = filepath.Clean(root)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

// defaultCacheDir returns where lazily extracted runtimes live when
// the app does not provide an explicit toolchain cache directory.
// OPEN_CRAFT_TOOLCHAIN_CACHE (used inside sandboxes) wins, then
// OPEN_CRAFT_DATA_DIR so tests and custom data roots stay isolated;
// otherwise runtimes land under ~/.opencraft/runtime.
func defaultCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv("OPEN_CRAFT_TOOLCHAIN_CACHE")); dir != "" {
		return dir
	}
	if dir := strings.TrimSpace(os.Getenv("OPEN_CRAFT_DATA_DIR")); dir != "" {
		return filepath.Join(dir, "runtime")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".opencraft", "runtime")
}

// Resolve resolves one bare tool name to an executable. Tools that
// are absolute paths or contain a path separator are the caller's
// business and rejected here.
func (m *Manager) Resolve(ctx context.Context, tool string) (*Runtime, error) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return nil, errors.New("runtimes: tool name is empty")
	}
	if filepath.IsAbs(tool) || strings.ContainsAny(tool, `/\`) {
		return nil, fmt.Errorf(
			"runtimes: %q is not a bare tool name", tool)
	}
	family, ok := toolFamilies[tool]
	if !ok {
		return nil, fmt.Errorf(
			"runtimes: unknown tool %q (want python/python3, go/gofmt, node/npm/npx, uv/uvx)",
			tool)
	}
	external := m.lookExternal(tool)
	_ = ctx
	switch {
	case m.preference == PreferenceOff:
		if external == nil {
			return nil, fmt.Errorf(
				"runtimes: %s is not available on PATH and bundled runtimes are off",
				tool)
		}
		return external, nil
	case external != nil && m.preference == PreferenceExternalFirst:
		return external, nil
	}
	// Only inspect/extract a bundled sidecar when the resolution
	// policy actually needs it. With external-first, an existing
	// system tool avoids touching the compressed runtime archive.
	bundled, bundledErr := m.lookBundled(family, tool)
	if bundledErr != nil {
		if external != nil {
			return external, nil
		}
		return nil, bundledErr
	}
	switch {
	case bundled != nil:
		return bundled, nil
	case external != nil:
		return external, nil
	default:
		return nil, fmt.Errorf(
			"runtimes: %s not found on PATH%s",
			tool, missingBundledHint(m.root, family))
	}
}

func missingBundledHint(root, family string) string {
	if root == "" {
		return " and no bundled runtime root is present"
	}
	return " and no bundled " + family + " runtime matches the manifest"
}

// lookExternal returns a system tool when it is on the external PATH
// and is not one of our own launcher shims.
func (m *Manager) lookExternal(tool string) *Runtime {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if m.launcherDir != "" && isSameDir(dir, m.launcherDir) {
			continue
		}
		for _, name := range toolCandidates(tool) {
			path, ok := lookPathIn(dir, name)
			if !ok {
				continue
			}
			return &Runtime{
				Tool:   tool,
				Family: toolFamilies[tool],
				Path:   path,
				Source: SourceSystem,
			}
		}
	}
	return nil
}

// lookBundled resolves one tool inside the staged sidecar. Runtimes
// may ship as compressed archives and are extracted into the user
// cache only when a bundled fallback is actually needed.
func (m *Manager) lookBundled(family, tool string) (*Runtime, error) {
	if m.preference == PreferenceOff || m.root == "" {
		return nil, nil
	}
	var entry *manifestEntry
	if m.manifest != nil {
		entry = m.manifest.entry(family)
	}
	version := ""
	var roots []string
	addRoot := func(base string) {
		if base == "" {
			return
		}
		roots = append(roots,
			filepath.Join(base, family, version, platformKey()))
		if minor := dirVersion(version, family); minor != "" && minor != version {
			roots = append(roots,
				filepath.Join(base, family, minor, platformKey()))
		}
	}
	if entry != nil {
		version = entry.Version
		addRoot(m.cacheDir)
		addRoot(m.root)
	} else {
		// Dev/test fallback: no manifest, look for a single staged
		// copy under <root>/<family>/<platform>.
		roots = append(roots, filepath.Join(m.root, family, platformKey()))
	}
	look := func() *Runtime {
		for _, dir := range roots {
			binDir := filepath.Join(dir, binRel(family))
			for _, name := range toolCandidates(tool) {
				path, ok := lookPathIn(binDir, name)
				if !ok {
					continue
				}
				return &Runtime{
					Tool:    tool,
					Family:  family,
					Version: version,
					Path:    path,
					BinDir:  binDir,
					Root:    dir,
					Source:  SourceBundled,
				}
			}
		}
		return nil
	}
	if rt := look(); rt != nil {
		return rt, nil
	}
	if entry == nil {
		return nil, nil
	}
	if err := m.ensureExtracted(family, nil); err != nil {
		return nil, err
	}
	return look(), nil
}

// PrepareBundled extracts every missing bundled runtime in the
// background. With the default external-first policy, families already
// available on PATH are skipped. progress receives per-family byte
// progress when extraction starts.
func (m *Manager) PrepareBundled(progress ProgressFunc) error {
	if m.preference == PreferenceOff || m.manifest == nil {
		return nil
	}
	var errs []error
	for _, item := range []struct {
		family string
		tool   string
	}{
		{family: "python", tool: "python"},
		{family: "node", tool: "node"},
		{family: "uv", tool: "uv"},
	} {
		if m.manifest.entry(item.family) == nil {
			continue
		}
		if m.preference == PreferenceExternalFirst &&
			m.lookExternal(item.tool) != nil {
			continue
		}
		if err := m.ensureExtracted(item.family, progress); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// toolCandidates returns executable names to try for one bare tool.
// macOS and python-build-standalone ship python3 without a python
// alias, so python resolves to python3 when python itself is absent.
func toolCandidates(tool string) []string {
	if tool == "python" {
		return []string{"python", "python3"}
	}
	return []string{tool}
}

// SandboxEnv returns the environment additions the exec sandbox needs:
// launcher PATH placement plus cache variables that are independent
// of which concrete runtime a launcher later selects.
func (m *Manager) SandboxEnv() map[string]string {
	out := map[string]string{
		"OPEN_CRAFT_TOOLCHAIN_PREFERENCE": string(m.preference),
	}
	if m.cacheDir != "" {
		out["OPEN_CRAFT_TOOLCHAIN_CACHE"] = m.cacheDir
	}
	if launcher := m.LauncherDir(); launcher != "" {
		out["PATH"] = prependPath(os.Getenv("PATH"), launcher)
	}
	cache := m.sandboxCacheDir
	if cache == "" {
		cache = os.Getenv("OPEN_CRAFT_CACHE")
	}
	if cache != "" {
		out["GOCACHE"] = filepath.Join(cache, "go")
		out["GOMODCACHE"] = filepath.Join(cache, "go", "pkg", "mod")
		out["PYTHONUSERBASE"] = filepath.Join(cache, "python")
		out["UV_CACHE_DIR"] = filepath.Join(cache, "uv")
		out["npm_config_cache"] = filepath.Join(cache, "npm")
	}
	return out
}

// HostEnv returns the environment additions for host-side MCP
// servers. Host caches deliberately live outside the sandbox cache.
func (m *Manager) HostEnv() map[string]string {
	cache := m.hostCacheDir
	if cache == "" {
		if dataDir := os.Getenv("OPEN_CRAFT_DATA_DIR"); dataDir != "" {
			cache = filepath.Join(dataDir, "mcp-cache")
		} else if home, err := os.UserHomeDir(); err == nil {
			cache = filepath.Join(home, ".opencraft", "mcp-cache")
		}
	}
	if cache == "" {
		return nil
	}
	return map[string]string{
		"UV_CACHE_DIR":     filepath.Join(cache, "uv"),
		"npm_config_cache": filepath.Join(cache, "npm"),
	}
}

// AttachHostEnv returns serverEnv with the manager's host cache
// variables filled in for keys the caller did not set explicitly.
func (m *Manager) AttachHostEnv(serverEnv map[string]string) map[string]string {
	return MergeEnv(serverEnv, m.HostEnv())
}

// Diagnose returns one status per supported tool command.
func (m *Manager) Diagnose(ctx context.Context) []RuntimeStatus {
	tools := []string{
		"python", "python3", "go", "node", "uv", "uvx", "npx",
	}
	out := make([]RuntimeStatus, 0, len(tools))
	for _, tool := range tools {
		rt, err := m.Resolve(ctx, tool)
		st := RuntimeStatus{Tool: tool}
		if err != nil {
			st.Error = err.Error()
		} else {
			st.Family = rt.Family
			st.Version = rt.Version
			st.Source = string(rt.Source)
			st.Path = rt.Path
		}
		out = append(out, st)
	}
	return out
}

// ResolveMCPCommand resolves a stdio MCP command while leaving
// absolute paths and path-containing commands untouched. It returns
// the executable path the transport should spawn.
func (m *Manager) ResolveMCPCommand(
	ctx context.Context,
	command string,
) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" || filepath.IsAbs(command) ||
		strings.ContainsAny(command, `/\`) {
		return command, nil
	}
	if _, managed := toolFamilies[command]; managed {
		rt, err := m.Resolve(ctx, command)
		if err != nil {
			return "", err
		}
		return rt.Path, nil
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf(
			"runtimes: %s not found on PATH", command)
	}
	return path, nil
}

// MergeEnv overlays additions onto base without replacing keys the
// caller already set (explicit user configuration wins).
func MergeEnv(base, additions map[string]string) map[string]string {
	if len(additions) == 0 {
		return base
	}
	out := make(map[string]string, len(base)+len(additions))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range additions {
		if _, exists := out[key]; !exists {
			out[key] = value
		}
	}
	return out
}

func isSameDir(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func platformKey() string {
	return runtime.GOOS + "-" + runtime.GOARCH
}

// dirVersion returns the version directory used by the packaged
// layout: Node versions use the major segment (24), Python/Go/uv use
// major.minor (3.13, 1.25, 0.12).
func dirVersion(version, family string) string {
	parts := strings.Split(version, ".")
	if len(parts) <= 1 {
		return ""
	}
	if family == "node" {
		return parts[0]
	}
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return ""
}

func prependPath(current, dir string) string {
	if current == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + current
}

func lookPathIn(dir, tool string) (string, bool) {
	if dir == "" {
		return "", false
	}
	var candidates []string
	if runtime.GOOS == "windows" {
		base := tool
		for _, ext := range []string{".exe", ".cmd", ".bat"} {
			base = strings.TrimSuffix(base, ext)
		}
		candidates = []string{
			base + ".exe", base + ".cmd", base + ".bat", base,
		}
	} else {
		candidates = []string{tool}
	}
	for _, name := range candidates {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			continue
		}
		return path, true
	}
	return "", false
}
