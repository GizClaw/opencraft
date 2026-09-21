package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEnv builds a getenv func over a map, so every resolution is
// hermetic and independent of the process environment.
func fakeEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestResolveLaunchDefaults(t *testing.T) {
	home := t.TempDir()
	launch, err := ResolveLaunch(nil, fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if launch.Profile != "" {
		t.Errorf("profile = %q, want empty", launch.Profile)
	}
	if want := filepath.Join(home, ".opencraft"); launch.DataDir != want {
		t.Errorf("state root = %q, want %q", launch.DataDir, want)
	}
	if launch.AppHome != launch.DataDir {
		t.Errorf("app home = %q, want the state root %q",
			launch.AppHome, launch.DataDir)
	}
	if want := filepath.Join(home, ".opencraft", "config"); launch.ConfigDir != want {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, want)
	}
	if launch.WorkDir != "" {
		t.Errorf("work dir = %q, want empty", launch.WorkDir)
	}
	if launch.AppName != BaseAppName {
		t.Errorf("app name = %q, want %q", launch.AppName, BaseAppName)
	}
	if !launch.SingleInstance {
		t.Error("single instance = false, want true")
	}
	if !strings.HasPrefix(launch.InstanceID, instanceIDPrefix) {
		t.Errorf("instance id = %q, want prefix %q",
			launch.InstanceID, instanceIDPrefix)
	}
	if got := strings.TrimPrefix(launch.InstanceID, instanceIDPrefix); len(got) != 12 {
		t.Errorf("instance id hash = %q, want 12 hex chars", got)
	}
	// Resolution is read-only: a launch that never starts must not seed
	// directories under the home it resolved.
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read home: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("resolution created %d entries under home, want none", len(entries))
	}
}

func TestResolveLaunchProfileKeepsAppHome(t *testing.T) {
	home := t.TempDir()
	launch, err := ResolveLaunch(nil, fakeEnv(map[string]string{
		"HOME":              home,
		EnvProfile:          "dev",
		EnvConfigDir:        "",
		EnvDataDir:          "",
		EnvAppHome:          "",
		EnvWorkDir:          "",
		EnvNoSingleInstance: "",
	}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if want := filepath.Join(home, ".opencraft-dev"); launch.DataDir != want {
		t.Errorf("state root = %q, want %q", launch.DataDir, want)
	}
	if want := filepath.Join(home, ".opencraft"); launch.AppHome != want {
		t.Errorf("app home = %q, want the shared root %q", launch.AppHome, want)
	}
	if want := filepath.Join(home, ".opencraft", "config"); launch.ConfigDir != want {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, want)
	}
	if want := "OpenCraft (dev)"; launch.AppName != want {
		t.Errorf("app name = %q, want %q", launch.AppName, want)
	}
}

func TestResolveLaunchPriority(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{
		"HOME":              home,
		EnvDataDir:          filepath.Join(home, "env-state"),
		EnvAppHome:          filepath.Join(home, "env-home"),
		EnvConfigDir:        filepath.Join(home, "env-config"),
		EnvWorkDir:          filepath.Join(home, "env-work"),
		EnvProfile:          "dev",
		EnvNoSingleInstance: "1",
	}
	launch, err := ResolveLaunch([]string{
		"--data-dir", filepath.Join(home, "flag-state"),
		"--app-home=" + filepath.Join(home, "flag-home"),
		"--config", filepath.Join(home, "flag-config"),
		"--workdir", filepath.Join(home, "flag-work"),
	}, fakeEnv(env))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{"state root", launch.DataDir, filepath.Join(home, "flag-state")},
		{"app home", launch.AppHome, filepath.Join(home, "flag-home")},
		{"config dir", launch.ConfigDir, filepath.Join(home, "flag-config")},
		{"work dir", launch.WorkDir, filepath.Join(home, "flag-work")},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if launch.SingleInstance {
		t.Error("single instance = true, want false from the env escape hatch")
	}
}

// TestResolveLaunchConfigDirAloneMovesBothRoots pins the historical
// `opencraft run --config DIR` rule: a config directory selected on its
// own takes the state root with it (DIR's parent). Without it, a scratch
// config would read that directory but write sessions, user.db and logs
// into the installed app's state root.
func TestResolveLaunchConfigDirAloneMovesBothRoots(t *testing.T) {
	home := t.TempDir()
	scratch := filepath.Join(home, "scratch")
	launch, err := ResolveLaunch([]string{
		"--config", filepath.Join(scratch, "config"),
	}, fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if want := filepath.Join(scratch, "config"); launch.ConfigDir != want {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, want)
	}
	if launch.DataDir != scratch {
		t.Errorf("state root = %q, want %q", launch.DataDir, scratch)
	}
	if launch.AppHome != scratch {
		t.Errorf("app home = %q, want the state root %q", launch.AppHome, scratch)
	}
}

func TestResolveLaunchExplicitRootsBeatConfigDir(t *testing.T) {
	home := t.TempDir()
	state := filepath.Join(home, "state")
	config := filepath.Join(home, "scratch", "config")
	launch, err := ResolveLaunch([]string{
		"--config", config,
		"--data-dir", state,
	}, fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if launch.DataDir != state {
		t.Errorf("state root = %q, want the explicit %q", launch.DataDir, state)
	}
	if launch.ConfigDir != config {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, config)
	}
}

// TestResolveLaunchProfileBeatsConfigDir keeps the profile rule intact:
// a profile always means "the shared app home, this profile's state",
// so an explicit config directory cannot pull the state root anywhere.
func TestResolveLaunchProfileBeatsConfigDir(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "scratch", "config")
	launch, err := ResolveLaunch([]string{
		"--profile", "dev",
		"--config", config,
	}, fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if want := filepath.Join(home, ".opencraft-dev"); launch.DataDir != want {
		t.Errorf("state root = %q, want %q", launch.DataDir, want)
	}
	if want := filepath.Join(home, ".opencraft"); launch.AppHome != want {
		t.Errorf("app home = %q, want the shared %q", launch.AppHome, want)
	}
	if launch.ConfigDir != config {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, config)
	}
}

func TestResolveLaunchEnvBeatsProfileDefaults(t *testing.T) {
	home := t.TempDir()
	stateRoot := filepath.Join(home, "custom-state")
	launch, err := ResolveLaunch(nil, fakeEnv(map[string]string{
		"HOME":       home,
		EnvProfile:   "dev",
		EnvDataDir:   stateRoot,
		EnvAppHome:   filepath.Join(home, "custom-home"),
		EnvConfigDir: filepath.Join(home, "custom-config"),
	}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if launch.DataDir != stateRoot {
		t.Errorf("state root = %q, want %q", launch.DataDir, stateRoot)
	}
	if want := filepath.Join(home, "custom-home"); launch.AppHome != want {
		t.Errorf("app home = %q, want %q", launch.AppHome, want)
	}
	if want := filepath.Join(home, "custom-config"); launch.ConfigDir != want {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, want)
	}
}

func TestResolveLaunchSelfContainedInstance(t *testing.T) {
	home := t.TempDir()
	stateRoot := filepath.Join(home, "oc-e2e")
	launch, err := ResolveLaunch(
		[]string{"--data-dir", stateRoot, "-psn_0_123", "--", "--ignored"},
		fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	// Without a profile the app home follows the state root, so nothing
	// outside the named directory is touched.
	if launch.AppHome != stateRoot {
		t.Errorf("app home = %q, want the state root %q", launch.AppHome, stateRoot)
	}
	if want := filepath.Join(stateRoot, "config"); launch.ConfigDir != want {
		t.Errorf("config dir = %q, want %q", launch.ConfigDir, want)
	}
}

func TestResolveLaunchTolerantScan(t *testing.T) {
	home := t.TempDir()
	launch, err := ResolveLaunch([]string{
		"-psn_0_123", "--prompt", "hi", "run", "--json", "--profile=dev",
	}, fakeEnv(map[string]string{"HOME": home}))
	if err != nil {
		t.Fatalf("ResolveLaunch: %v", err)
	}
	if launch.Profile != "dev" {
		t.Errorf("profile = %q, want dev", launch.Profile)
	}
}

func TestResolveLaunchRejectsBadArguments(t *testing.T) {
	home := t.TempDir()
	env := fakeEnv(map[string]string{"HOME": home})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing value", []string{"--data-dir"}},
		{"empty value", []string{"--app-home="}},
		{"empty profile", []string{"--profile="}},
		{"profile with slash", []string{"--profile", "dev/prod"}},
		{"profile with dot", []string{"--profile", ".."}},
		{"profile with space", []string{"--profile", "dev prod"}},
		{"long profile", []string{"--profile", strings.Repeat("a", 65)}},
	} {
		if _, err := ResolveLaunch(tc.args, env); err == nil {
			t.Errorf("%s: ResolveLaunch accepted %v", tc.name, tc.args)
		}
	}
}

func TestInstanceIDFollowsCanonicalStateRoot(t *testing.T) {
	// /tmp is a symlink on macOS; both spellings must land on one id.
	first := InstanceIDFor("/tmp/../tmp/opencraft-id-test")
	second := InstanceIDFor("/tmp/opencraft-id-test")
	if first != second {
		t.Errorf("instance id differs for equivalent roots: %q vs %q", first, second)
	}
	other := InstanceIDFor("/tmp/opencraft-id-test-other")
	if other == first {
		t.Errorf("different roots share instance id %q", first)
	}

	// A root that does not exist yet resolves through its deepest
	// existing ancestor, so two spellings of one missing directory
	// still agree.
	home := t.TempDir()
	missing := filepath.Join(home, "not-there", "state")
	noisy := filepath.Join(home, "not-there", "..", "not-there", "state")
	if got, want := InstanceIDFor(noisy), InstanceIDFor(missing); got != want {
		t.Errorf("unresolved root ids differ: %q vs %q", got, want)
	}
}

func TestAppNameForProfile(t *testing.T) {
	if got := AppNameFor(""); got != BaseAppName {
		t.Errorf("AppNameFor(%q) = %q, want %q", "", got, BaseAppName)
	}
	if got, want := AppNameFor("dev"), "OpenCraft (dev)"; got != want {
		t.Errorf("AppNameFor(dev) = %q, want %q", got, want)
	}
}
