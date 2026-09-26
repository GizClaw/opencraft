package platform_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// goosSite is one allowlisted file plus why it is allowed to read the
// host OS, and how many reads it may contain.
//
// W8 of docs/architecture-plan.md: reading runtime.GOOS is the cheapest
// possible way to answer a platform question and therefore the easiest
// thing to scatter. W1 converged the questions themselves (backend,
// shells, PATH, hidden marking); this list keeps the answers converged:
// a new read has to be added here, with a reason a reviewer reads, or
// the test fails.
//
// max is a ceiling, not an exact count: a site may disappear from a
// file without the reason being wrong, but a file may not grow a new
// branch without a deliberate edit to this table. Counts are sites in
// code — a mention inside a comment does not count.
type goosSite struct {
	max    int
	reason string
}

// goosAllowlist is the whole surface. Each entry points at the part of
// the platform matrix (§4) or the package that owns the decision; if a
// reason reads like "it needs the OS here" with no owner, the read
// belongs upstream in internal/foundation/platform instead.
var goosAllowlist = map[string]goosSite{
	// The sandbox: one place picks the confined backend and one
	// shapes the YOLO runner around the same choice (§4 rows 1-3).
	"internal/capabilities/sandbox/backend.go": {
		max: 1,
		reason: "sandbox.Backend/newConfinedRunner: the single " +
			"backend chooser (W1.1). Every other caller asks it.",
	},
	"internal/capabilities/sandbox/sandboxpm.go": {
		max: 1,
		reason: "UnconfinedRunner: YOLO on Windows goes through the " +
			"job-object backend without write confinement (§4 row 3), " +
			"which is a different runner from sandboxlocal.",
	},
	// The host shell and the tool list that follows from it.
	"internal/capabilities/tools/register.go": {
		max: 1,
		reason: "the exec tool list is host-specific: Windows has no " +
			"interactive sessions (issue #38, §4 row 4), so exec_session " +
			"is not offered there. Same value sandbox.noTTYRunner reads.",
	},
	"internal/capabilities/tools/exec/command.go": {
		max: 1,
		reason: "shelldetect.Detect(goos): the default shell is a host " +
			"fact (§4 row 5); the rules themselves live in the platform " +
			"package.",
	},
	// Plugin packaging is OS-shaped: bundle layout and codesigning.
	"internal/capabilities/plugins/runtime/runtime.go": {
		max: 1,
		reason: "BuiltinPluginRoot: a macOS .app keeps plugins under " +
			"Contents/Resources/plugins, every other platform keeps " +
			"plugins/ next to the binary (§4 row '插件 bundle 根').",
	},
	"internal/capabilities/plugins/plugin.go": {
		max: 1,
		reason: "signAdHoc: an unsigned Mach-O under ~/.opencraft is " +
			"SIGKILLed on exec, so macOS ad-hoc signs; no-op elsewhere.",
	},
	// PATH/HOME candidates: the package's whole job is to answer per
	// OS (§4 row 6).
	"internal/foundation/platform/envpath/envpath.go": {
		max: 2,
		reason: "Options.GOOS defaults to the host, and the Windows " +
			"installer-root candidates are Windows-only.",
	},
	// gh: the binary name and its config directory differ on Windows.
	"internal/foundation/utils/gitx/gh/service.go": {
		max: 2,
		reason: "the gh executable (gh.exe) and its config directory " +
			"are named per OS; the rest of the package is shared.",
	},
	// The desktop shell's own platform bits: window chrome, the
	// native menu, the notification authorization prompt.
	"main.go": {
		max: 2,
		reason: "the window shell: frameless chrome is for " +
			"Windows/Linux only (macOS keeps its traffic lights), and " +
			"the Wails single-instance machinery needs a session bus on " +
			"Linux (§4 row '菜单栏 / 托盘' is the related manual check).",
	},
	"internal/adapters/desktop/menu.go": {
		max: 1,
		reason: "the application menu exists only on macOS (§4 row " +
			"'菜单栏 / 托盘'); elsewhere the frontend's own UI is the menu.",
	},
	"internal/adapters/desktop/desktop.go": {
		max: 1,
		reason: "RequestNotificationAuthorization only prompts on " +
			"macOS (§4 row '系统通知授权弹窗').",
	},
	// The diagnostics report answers about this machine: goos, the
	// host shell, the backend in force.
	"internal/adapters/desktop/bindings/diagnostics.go": {
		max: 3,
		reason: "the report's subject *is* the host: its goos, its " +
			"shell, and the backend sandbox.Backend names (W1.2).",
	},
	// File hand-off and file-dialog paths: the argv table lives in
	// open.go (W1.3); what remains is passing the host in, plus the
	// Windows URL-form drive path.
	"internal/adapters/desktop/bindings/file.go": {
		max: 5,
		reason: "openWith call sites pass the host goos to the open.go " +
			"argv table (W1.3), and a file:// URL of a Windows drive " +
			"path carries a leading slash that is not part of the path.",
	},
	// MCP command validation: Windows has neither an execute bit nor
	// a single path separator, and never runs a shebang.
	"internal/adapters/desktop/bindings/mcp_command.go": {
		max: 3,
		reason: "bare-name detection, the executable-bit check and the " +
			"shebang check all have a Windows form because Windows has " +
			"neither an execute bit nor shebang execution.",
	},
}

// TestHostOSReadsAreAllowlisted is W8's gate: production code may read
// runtime.GOOS only in the files listed above, and only up to their
// cap. It scans the repo rather than a package list so the gate also
// covers main.go and future top-level commands.
func TestHostOSReadsAreAllowlisted(t *testing.T) {
	root := repoRoot(t)
	found := scanGOOSReads(t, root)

	for path := range scannedOrAllowlisted(found) {
		site, ok := goosAllowlist[path]
		count := found[path]
		switch {
		case !ok && count > 0:
			t.Errorf("%s: %d runtime.GOOS read(s) not in the "+
				"allowlist: either move the decision into "+
				"internal/foundation/platform (a goos parameter is "+
				"testable on every host) or add an entry here with the "+
				"reason it must know the OS (W8)", path, count)
		case ok && count == 0:
			t.Errorf("%s: allowlisted (%s) but reads the host OS "+
				"nowhere: drop the entry", path, site.reason)
		case count > site.max:
			t.Errorf("%s: %d runtime.GOOS reads, allowlist allows "+
				"%d (%s): raise the cap deliberately or fold the new "+
				"branch into one of the existing ones",
				path, count, site.max, site.reason)
		}
	}
	// A table that stopped matching anything would pass silently.
	if len(found) == 0 {
		t.Fatal("the GOOS scan found no reads at all: check the pattern")
	}
	t.Logf("host OS reads: %d files, %d sites", len(found), sum(found))
}

// scannedOrAllowlisted returns the union of both sets so the loop above
// reports allowlist rot as well as new reads.
func scannedOrAllowlisted(found map[string]int) map[string]int {
	out := make(map[string]int, len(found)+len(goosAllowlist))
	for path, count := range found {
		out[path] = count
	}
	for path := range goosAllowlist {
		if _, ok := out[path]; !ok {
			out[path] = 0
		}
	}
	return out
}

func sum(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// scanGOOSReads counts `runtime.GOOS` / `goruntime.GOOS` in the code
// part of every production Go file under root, keyed by slash path.
//
// Comment mentions do not count: the package whose whole job is to
// answer per OS documents itself that way, and a documentation
// reference is not a platform branch.
func scanGOOSReads(t *testing.T, root string) map[string]int {
	t.Helper()
	out := map[string]int{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			// Vendored trees and generated output. The repo-root
			// plugins/ directory holds the reference plugins (JS), so
			// it is skipped by path — internal/capabilities/plugins is
			// production Go and stays in scope.
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			slashRel := filepath.ToSlash(rel)
			switch entry.Name() {
			case ".git", ".opencraft", ".task", "node_modules":
				return fs.SkipDir
			}
			switch slashRel {
			case "frontend", "build", "dist", "Casks", "plugins", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") ||
			strings.HasSuffix(name, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		count := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			count += strings.Count(line, "runtime.GOOS")
		}
		if count == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = count
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// repoRoot walks up from this file until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", file)
		}
		dir = parent
	}
}

// TestAllowlistReasonsNameAnOwner keeps the table reviewable: every
// entry has to say who owns the decision, so a future reader can tell
// whether the read still belongs there.
func TestAllowlistReasonsNameAnOwner(t *testing.T) {
	paths := make([]string, 0, len(goosAllowlist))
	for path := range goosAllowlist {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		site := goosAllowlist[path]
		if site.max < 1 {
			t.Errorf("%s: max must be at least 1", path)
		}
		if len(strings.TrimSpace(site.reason)) < 30 {
			t.Errorf("%s: the reason is too short to be useful: %q",
				path, site.reason)
		}
		if !strings.HasSuffix(path, ".go") || filepath.IsAbs(path) {
			t.Errorf("%s: the key must be a repo-relative .go path", path)
		}
	}
	t.Logf("%d allowlisted files", len(goosAllowlist))
}
