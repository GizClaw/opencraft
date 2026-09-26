// Package envpath resolves the process PATH once at startup.
//
// The desktop app is usually launched from Finder/Dock, and a
// LaunchServices launch inherits launchd's minimal PATH
// (/usr/bin:/bin:/usr/sbin:/sbin) instead of the PATH a terminal
// session builds from the user's shell profile. Everything the process
// spawns inherits that: MCP servers resolved with exec.LookPath, the
// commands an agent runs through the sandbox (whose env policy filters
// the process environment), the gh/git/codesign lookups the app does
// itself. Tools installed by Homebrew, npm-global or a user-local
// prefix therefore "exist in the terminal but not in the app".
//
// Resolution runs before any spawn and rewrites the process environment
// in place, because third-party spawn sites (flowcraft's MCP stdio
// transport calls exec.Command on the bare name) resolve against the
// process PATH and cannot be reached by an explicit env argument.
//
// The merge rule is deliberately conservative — it can only add:
//
//  1. Prepend: user-configured directories, in the order given. They are
//     the only way to express "this directory wins", so they come first.
//  2. Inherited PATH: kept as-is, in order, minus empty entries.
//  3. Candidates: the platform's standard install directories, appended
//     after the inherited entries so a candidate can never shadow a
//     system tool the app already resolved.
//
// Nothing is ever removed except empty entries, and only absolute
// directory entries are accepted in Prepend. Version-manager
// directories (fnm/nvm multishell shims and friends) are intentionally
// not guessed: the active version directory is per-shell state, so the
// user supplies it through Prepend instead.
//
// The package owns the merge policy and the single os.Setenv call.
// Logging, persistence and the diagnostics surface stay with callers.
package envpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Segment sources reported for one resolved PATH entry.
const (
	// SourcePrepend marks a user-configured directory.
	SourcePrepend = "prepend"
	// SourceInherited marks a directory kept from the inherited PATH.
	SourceInherited = "inherited"
	// SourceCandidate marks a platform install directory that was added.
	SourceCandidate = "candidate"
)

// Options configures one Install call. Zero values resolve against the
// running process's environment and platform, which is what production
// callers want; the remaining fields exist so tests (and a future
// per-deployment override) can drive resolution without touching the
// host.
type Options struct {
	// Prepend lists directories to place in front of the inherited
	// PATH. Relative or empty entries are rejected rather than applied.
	Prepend []string
	// Candidates overrides the platform install directories. A nil
	// slice means Candidates(GOOS, HomeDir).
	Candidates []string
	// HomeDir overrides the home directory used to build the candidate
	// list. Empty means os.UserHomeDir.
	HomeDir string
	// GOOS overrides the platform the candidate list is built for.
	// Empty means runtime.GOOS.
	GOOS string
}

// Segment is one resolved PATH directory and where it came from.
type Segment struct {
	Dir     string `json:"dir"`
	Source  string `json:"source"`
	Present bool   `json:"present"`
}

// Plan is a resolved PATH with the provenance the diagnostics view
// reports.
type Plan struct {
	// Path is the joined PATH value.
	Path string `json:"path"`
	// Segments lists every entry of Path in order.
	Segments []Segment `json:"segments"`
	// Rejected lists Prepend entries that were not absolute (or not a
	// directory) and were therefore dropped.
	Rejected []string `json:"rejected"`
	// Missing lists candidate directories that do not exist on this
	// machine and were therefore not added.
	Missing []string `json:"missing"`
}

// Result reports what Install resolved and whether it rewrote PATH.
type Result struct {
	Plan
	// Inherited is the PATH the process had before the call.
	Inherited string `json:"inherited"`
	// Changed reports whether the process PATH was rewritten. An
	// unchanged PATH is left untouched so an install that found
	// everything already in place does not touch the environment.
	Changed bool `json:"changed"`
}

// Dirs returns the resolved directories that came from one source, in
// PATH order.
func (r Result) Dirs(source string) []string {
	var dirs []string
	for _, segment := range r.Segments {
		if segment.Source == source {
			dirs = append(dirs, segment.Dir)
		}
	}
	return dirs
}

// Install resolves the process PATH and writes it back when the result
// differs. It is idempotent: resolving an already-merged PATH yields the
// same value, so the execd child (which re-runs it on the PATH its
// parent handed down) cannot grow or reorder the list.
func Install(opts Options) (Result, error) {
	inherited := os.Getenv("PATH")
	result := Result{Inherited: inherited}
	result.Plan = Inspect(opts)
	if result.Path == inherited {
		return result, nil
	}
	if err := os.Setenv("PATH", result.Path); err != nil {
		return result, err
	}
	result.Changed = true
	return result, nil
}

// Inspect resolves the process PATH without writing it back, using the
// same options as Install. Diagnostics call it to describe what the
// current environment resolves to when a startup install never ran.
func Inspect(opts Options) Plan {
	home := opts.HomeDir
	if home == "" {
		// An unresolvable home directory only costs the candidate
		// entries that hang off it; resolution still proceeds.
		if resolved, err := os.UserHomeDir(); err == nil {
			home = resolved
		}
	}
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	candidates := opts.Candidates
	if candidates == nil {
		candidates = Candidates(goos, home)
	}
	return Resolve(os.Getenv("PATH"), opts.Prepend, candidates)
}

// Resolve merges the inherited PATH with the prepend list and the
// candidate directories. The first occurrence of a directory wins:
// naming an inherited directory in Prepend therefore promotes it to the
// front rather than adding a second copy.
func Resolve(inherited string, prepend, candidates []string) Plan {
	plan := Plan{}
	seen := make(map[string]bool)
	add := func(dir, source string) {
		dir = filepath.Clean(dir)
		key := dir
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			return
		}
		seen[key] = true
		plan.Segments = append(plan.Segments, Segment{
			Dir:     dir,
			Source:  source,
			Present: isDir(dir),
		})
	}
	for _, dir := range prepend {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		// Only absolute directories: a relative entry would resolve
		// against whatever working directory the app happens to use,
		// and a file on PATH is meaningless.
		if !filepath.IsAbs(dir) || (exists(dir) && !isDir(dir)) {
			plan.Rejected = append(plan.Rejected, dir)
			continue
		}
		// A missing directory stays: it is valid to point PATH at a
		// prefix that gets installed later, and the segment is reported
		// as not present.
		add(dir, SourcePrepend)
	}
	for _, dir := range filepath.SplitList(inherited) {
		// Empty entries mean "the working directory", which is both a
		// surprise and a hazard for a desktop app that spawns from the
		// user's workspace; drop them.
		if dir == "" {
			continue
		}
		add(dir, SourceInherited)
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if !isDir(dir) {
			plan.Missing = append(plan.Missing, dir)
			continue
		}
		add(dir, SourceCandidate)
	}
	dirs := make([]string, 0, len(plan.Segments))
	for _, segment := range plan.Segments {
		dirs = append(dirs, segment.Dir)
	}
	plan.Path = strings.Join(dirs, string(filepath.ListSeparator))
	return plan
}

// Candidates lists the standard user-facing install directories for a
// platform. Callers append them after the inherited PATH; the list is
// not ordered by precedence.
//
// The list is per-platform on purpose: the diagnostics view reports a
// candidate that is absent as a signal, so naming /snap/bin on macOS (or
// /opt/homebrew/bin on Linux) would be noise rather than information.
//
// Windows is deliberately empty: a GUI launch inherits the user's
// environment from the registry, so the PATH it sees is already the
// user's PATH.
func Candidates(goos, home string) []string {
	if goos == "windows" {
		return nil
	}
	var candidates []string
	if goos == "darwin" {
		// Apple Silicon Homebrew, then the classic Intel prefix (which
		// is also where a manual install lands).
		candidates = []string{"/opt/homebrew/bin", "/usr/local/bin"}
	} else {
		candidates = []string{
			"/usr/local/bin",
			// Ubuntu's snap packages.
			"/snap/bin",
			// The prefix the Linuxbrew installer recommends.
			"/home/linuxbrew/.linuxbrew/bin",
		}
	}
	if home != "" {
		if goos != "darwin" {
			// Linuxbrew installed into the home directory.
			candidates = append(candidates,
				filepath.Join(home, ".linuxbrew", "bin"))
		}
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin"),
			filepath.Join(home, "bin"),
			// Go and Rust install into the home directory by default.
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			// pyenv keeps stable shims, so this one is safe to guess
			// (version-manager multishell directories are not).
			filepath.Join(home, ".pyenv", "shims"),
		)
	}
	return candidates
}

// exists reports whether path is present at all.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// isDir reports whether path is an existing directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
