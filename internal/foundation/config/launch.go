package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The launch resolver turns one process start into the pair of roots the
// rest of the app runs against. Two roots exist because they answer
// different questions (docs/multi-instance-and-profiles.md §2):
//
//   - state root: everything with session state (workspaces/, user.db,
//     logs/, audit/, cache/). One GUI process per state root.
//   - app home: user content and credentials (config/, keyring/,
//     plugins/, agents/, skills/). Shared by every process that belongs
//     to the same user.
//
// A profile ("--profile dev") keeps the app home and moves the state
// root, which is how a dev copy runs next to the installed app without
// inheriting its sessions. A bare "--data-dir" moves both, which is the
// self-contained instance CI and e2e want.
const (
	// EnvProfile selects a named profile when no --profile flag is
	// given. It only supplies defaults; an explicit flag always wins.
	EnvProfile = "OPENCRAFT_PROFILE"
	// EnvDataDir overrides the state root.
	EnvDataDir = "OPENCRAFT_DATA_DIR"
	// EnvAppHome overrides the app home (config, keyring, plugins).
	EnvAppHome = "OPENCRAFT_APP_HOME"
	// EnvConfigDir overrides the configuration directory.
	EnvConfigDir = "OPENCRAFT_CONFIG_DIR"
	// EnvWorkDir overrides the startup workspace.
	EnvWorkDir = "OPENCRAFT_WORKDIR"
	// EnvNoSingleInstance lets two GUI processes share one state root.
	// The duplicate schedulers and the two writers on user.db are then
	// the user's own risk; it exists for debugging the lock itself.
	EnvNoSingleInstance = "OPENCRAFT_NO_SINGLE_INSTANCE"
)

const (
	// BaseAppName is the product name; the window title, the tray and
	// the native notifications use it unless a profile renames the
	// instance.
	BaseAppName = "OpenCraft"
	// defaultDataDirName is the state root under $HOME: ~/.opencraft.
	defaultDataDirName = ".opencraft"
	// instanceIDPrefix leads the single-instance id. The trailing "d"
	// keeps every D-Bus name element from starting with a digit.
	instanceIDPrefix = "com.GizClaw.opencraft.d"
	// instanceIDHashBytes is how much of the SHA-256 digest of the
	// canonical state root goes into the instance id (24 hex chars of
	// headroom are not needed; 12 is plenty for a handful of roots).
	instanceIDHashBytes = 6
	// maxProfileNameLength bounds a profile name; it ends up in a
	// directory name and a D-Bus name element.
	maxProfileNameLength = 64
)

// Launch is the resolved shape of one process start. Every field is
// already absolute (or empty when nothing selected one), so callers
// never resolve a path themselves.
type Launch struct {
	// Profile is the selected profile name, empty for the default one.
	Profile string
	// DataDir is the state root. One GUI process per state root.
	DataDir string
	// AppHome is the shared content/credential root.
	AppHome string
	// ConfigDir is where opencraft.yaml, desktop.json and hooks.json
	// live; it defaults to <AppHome>/config.
	ConfigDir string
	// WorkDir is the startup workspace, empty when the registry
	// (desktop) or the current directory (headless) decides.
	WorkDir string
	// AppName is the product name with the profile suffix applied.
	AppName string
	// InstanceID is the single-instance identity derived from the
	// canonical state root.
	InstanceID string
	// SingleInstance reports whether this process must hold the
	// per-state-root GUI mutex.
	SingleInstance bool
}

// ResolveLaunch resolves the roots and the single-instance identity from
// a tolerant scan of args plus getenv. It is a pure function: it reads
// nothing but the injected environment and creates nothing, so tests can
// point $HOME at a temporary directory and observe every resolution.
//
// The scan is deliberately tolerant of unknown arguments instead of
// using flag.Parse: macOS hands Finder launches a "-psn_0_123" argument,
// and flag's strict mode would abort the app on it.
//
// The priority table is: --flag > OPENCRAFT_* environment > profile
// default > home default, per value. The one cross-value rule is the
// historical `--config DIR` one: a config directory selected while
// nothing selected a state root moves the state root to DIR's parent,
// which is what makes `opencraft run --config /tmp/scratch` a
// self-contained instance instead of a split one.
func ResolveLaunch(args []string, getenv func(string) string) (Launch, error) {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	flags, err := scanLaunchArgs(args)
	if err != nil {
		return Launch{}, err
	}

	profile := flags.profile
	if profile == "" {
		profile = strings.TrimSpace(getenv(EnvProfile))
	}
	if profile != "" {
		if err := ValidateProfile(profile); err != nil {
			return Launch{}, err
		}
	}

	home, err := homeDir(getenv)
	if err != nil {
		return Launch{}, err
	}
	defaultRoot := filepath.Join(home, defaultDataDirName)

	dataDir := firstNonEmpty(flags.dataDir, getenv(EnvDataDir))
	configDir := firstNonEmpty(flags.configDir, getenv(EnvConfigDir))
	if dataDir == "" {
		switch {
		case profile != "":
			dataDir = defaultRoot + "-" + profile
		case configDir != "":
			// The historical `opencraft run --config DIR` rule: the
			// config directory's parent is the state root, so pointing
			// the config at a scratch directory moves the whole
			// instance there instead of mixing one root's config with
			// another root's sessions. A profile keeps its own root
			// (one user, another state), and an explicit --data-dir
			// wins outright.
			dataDir = filepath.Dir(configDir)
		default:
			dataDir = defaultRoot
		}
	}
	if dataDir, err = absolute(dataDir); err != nil {
		return Launch{}, fmt.Errorf("config: state root: %w", err)
	}

	appHome := firstNonEmpty(flags.appHome, getenv(EnvAppHome))
	if appHome == "" {
		// A profile means "the same user, another set of state", so the
		// app home stays the shared one. A bare state root (or config
		// directory) override means "self-contained instance", so the
		// app home follows it.
		appHome = dataDir
		if profile != "" {
			appHome = defaultRoot
		}
	}
	if appHome, err = absolute(appHome); err != nil {
		return Launch{}, fmt.Errorf("config: app home: %w", err)
	}

	if configDir == "" {
		configDir = filepath.Join(appHome, "config")
	}
	if configDir, err = absolute(configDir); err != nil {
		return Launch{}, fmt.Errorf("config: config dir: %w", err)
	}

	workDir := firstNonEmpty(flags.workDir, getenv(EnvWorkDir))
	if workDir != "" {
		if workDir, err = absolute(workDir); err != nil {
			return Launch{}, fmt.Errorf("config: work dir: %w", err)
		}
	}

	return Launch{
		Profile:        profile,
		DataDir:        dataDir,
		AppHome:        appHome,
		ConfigDir:      configDir,
		WorkDir:        workDir,
		AppName:        AppNameFor(profile),
		InstanceID:     InstanceIDFor(dataDir),
		SingleInstance: !truthy(getenv(EnvNoSingleInstance)),
	}, nil
}

// AppNameFor renders the product name for one profile: the base name
// for the default profile, "OpenCraft (dev)" for "dev". Native surfaces
// (window title, tray, notifications) use it so a dev window is never
// mistaken for the installed app.
func AppNameFor(profile string) string {
	if profile == "" {
		return BaseAppName
	}
	return fmt.Sprintf("%s (%s)", BaseAppName, profile)
}

// InstanceIDFor derives the single-instance id from one state root. The
// canonical path is hashed rather than embedded: the id shows up in
// $TMPDIR/<id>.lock, in a D-Bus name and in a Windows mutex name, none
// of which should carry a filesystem path (or run into their length and
// character limits). The consequence is the invariant the desktop needs:
// one GUI per state root, any number of state roots side by side.
func InstanceIDFor(stateRoot string) string {
	canonical := canonicalPath(stateRoot)
	sum := sha256.Sum256([]byte(canonical))
	hash := hex.EncodeToString(sum[:instanceIDHashBytes])
	return instanceIDPrefix + hash
}

// ValidateProfile rejects profile names that cannot be a directory
// suffix: anything outside [A-Za-z0-9_-], anything that could escape the
// home directory, and names long enough to bother a D-Bus name.
func ValidateProfile(profile string) error {
	if profile == "" {
		return fmt.Errorf("config: profile name is empty")
	}
	if len(profile) > maxProfileNameLength {
		return fmt.Errorf("config: profile %q is longer than %d characters",
			profile, maxProfileNameLength)
	}
	for _, r := range profile {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf(
				"config: profile %q may only contain letters, digits, '-' and '_'",
				profile)
		}
	}
	return nil
}

// launchFlags is the raw outcome of the tolerant argument scan: every
// field is empty when the argument was absent.
type launchFlags struct {
	profile   string
	dataDir   string
	appHome   string
	configDir string
	workDir   string
}

// scanLaunchArgs walks args once, picking up the launch flags and
// ignoring everything else (`-psn_0_123`, positional paths, flags that
// belong to another mode such as `run --prompt`). Scanning stops at "--".
func scanLaunchArgs(args []string) (launchFlags, error) {
	var flags launchFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		target := func() *string {
			switch name {
			case "--profile":
				return &flags.profile
			case "--data-dir":
				return &flags.dataDir
			case "--app-home":
				return &flags.appHome
			case "--config-dir", "--config":
				return &flags.configDir
			case "--workdir":
				return &flags.workDir
			}
			return nil
		}()
		if target == nil {
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return launchFlags{},
					fmt.Errorf("config: %s requires a value", name)
			}
			i++
			value = args[i]
		}
		if strings.TrimSpace(value) == "" {
			return launchFlags{},
				fmt.Errorf("config: %s requires a value", name)
		}
		*target = value
	}
	return flags, nil
}

// homeDir resolves the home directory from the injected environment
// first, so a test can redirect it, and from the OS otherwise.
func homeDir(getenv func(string) string) (string, error) {
	if home := strings.TrimSpace(getenv("HOME")); home != "" {
		return home, nil
	}
	if home := strings.TrimSpace(getenv("USERPROFILE")); home != "" {
		return home, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve home directory: %w", err)
	}
	return home, nil
}

// absolute cleans a path and makes it absolute without requiring it to
// exist.
func absolute(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// canonicalPath resolves symlinks for the deepest existing ancestor of
// path and rejoins the rest, so "/tmp/../tmp/x" and "/tmp/x" hash to the
// same instance id even before the directory exists. A path that cannot
// be resolved falls back to its cleaned absolute form.
func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	abs = filepath.Clean(abs)
	rest := ""
	current := abs
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			if rest == "" {
				return resolved
			}
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return abs
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
}

// firstNonEmpty returns the first trimmed non-empty value.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// truthy interprets an environment value as a boolean switch.
func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
