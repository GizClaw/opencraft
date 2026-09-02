package toolchain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LauncherRoot returns the bundled runtime root when the running
// executable is the runtime launcher itself (placed under
// <root>/launcher/). For the app binary it falls back to BundledRoot.
func LauncherRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return BundledRoot()
	}
	dir := filepath.Dir(exe)
	if filepath.Base(dir) == "launcher" {
		root := filepath.Clean(filepath.Dir(dir))
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
	}
	return BundledRoot()
}

// Launch resolves tool and replaces the current process with the
// selected executable. Bundled Go gets GOROOT/GOTOOLCHAIN only when
// the caller did not set them; external tools run with the inherited
// environment untouched.
func (m *Manager) Launch(tool string, args []string) error {
	rt, err := m.Resolve(context.Background(), tool)
	if err != nil {
		return err
	}
	env := os.Environ()
	if rt.Source == SourceBundled {
		env = withLaunchEnv(env, launchEnvFor(rt))
	}
	return execTool(rt.Path, tool, args, env)
}

func launchEnvFor(rt *Runtime) map[string]string {
	if rt.Family != "go" {
		return nil
	}
	out := map[string]string{}
	if rt.Root != "" {
		out["GOROOT"] = rt.Root
	}
	out["GOTOOLCHAIN"] = "local"
	return out
}

func withLaunchEnv(base []string, additions map[string]string) []string {
	if len(additions) == 0 {
		return base
	}
	values := make(map[string]string, len(base)+len(additions))
	for _, kv := range base {
		key, value, ok := strings.Cut(kv, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range additions {
		if _, exists := values[key]; !exists {
			values[key] = value
		}
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

// LauncherMain implements the shared launcher entrypoint. The tool is
// taken from argv[0] (symlink name); when the binary is invoked by its
// real name, the first argument names the tool.
func LauncherMain(argv []string) int {
	tool := filepath.Base(argv[0])
	tool = strings.TrimSuffix(tool, ".exe")
	var args []string
	if strings.HasPrefix(tool, "runtime-launcher") ||
		strings.HasPrefix(tool, "launcher") {
		if len(argv) < 2 {
			fmt.Fprintln(os.Stderr, "usage: runtime-launcher <tool> [args...]")
			return 2
		}
		tool = argv[1]
		args = argv[2:]
	} else {
		args = argv[1:]
	}
	pref, err := NormalizePreference(os.Getenv("OPEN_CRAFT_TOOLCHAIN_PREFERENCE"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-launcher:", err)
		return 1
	}
	root := LauncherRoot()
	manifestPath := ""
	if candidate := filepath.Join(root, "manifest.json"); root != "" {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			manifestPath = candidate
		}
	}
	m, err := New(Options{
		Preference:   pref,
		Root:         root,
		ManifestPath: manifestPath,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "runtime-launcher:", err)
		return 1
	}
	if err := m.Launch(tool, args); err != nil {
		fmt.Fprintln(os.Stderr, "runtime-launcher:", err)
		return 1
	}
	return 0
}
