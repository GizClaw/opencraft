// Target: what one Host serves, and the pool's unit of identity.
// Everything the manager used to key by a workspace path is keyed by a
// Target instead, so a user workspace and an installed application can
// never land in the same slot — two scopes sharing one string namespace
// is the one thing the application work could not be built on.

package host

import (
	"path/filepath"
	"strings"
)

// TargetKind names the namespace a Target's ID lives in.
type TargetKind string

const (
	// TargetWorkspace is a user workspace: ID is its cleaned work
	// directory.
	TargetWorkspace TargetKind = "ws"
	// TargetApp is an installed application: ID is the application id.
	TargetApp TargetKind = "app"
)

// Target names the thing one Host serves: a user workspace (ID is the
// cleaned work dir) or an installed application (ID is its id).
//
// It is how the pool, the deferred-replacement machine and the
// per-target counters tell their entries apart (see Key), and the
// identity every lifecycle guard is scoped by: asking for a Host means
// naming a target, never whatever the window happens to show.
//
// The zero Target is invalid, and both constructors produce it from a
// blank input: an unnamed workspace or app must not be able to borrow a
// real target's identity.
type Target struct {
	Kind TargetKind
	ID   string
}

// SameTarget reports whether two targets name the same thing. A target
// that names nothing matches nothing: an unresolved workspace must not
// borrow another target's identity, and the same string in another
// scope is another target. It is the one comparison every layer should
// ask — two spellings that this reports equal must also key equal, or
// one directory ends up with two Hosts.
func SameTarget(a, b Target) bool {
	return a.Valid() && b.Valid() && a == b
}

// WorkspaceTarget returns the target that serves one workspace
// directory. A blank path yields the zero Target: "no workspace" is not
// a workspace, and filepath.Clean would otherwise turn it into ".".
//
// A path that is literally "." is a directory the caller named — the
// process's own, which is what the guard above is there to keep out —
// and it keys as ".". Producers resolve an absolute path before they
// get here (the headless runner a working directory, the desktop the
// workspace the window shows, automations a validated absolute path),
// so this is the arm of the constructor a programming error reaches,
// not a way in.
func WorkspaceTarget(workDir string) Target {
	if strings.TrimSpace(workDir) == "" {
		return Target{}
	}
	return Target{Kind: TargetWorkspace, ID: filepath.Clean(workDir)}
}

// AppTarget returns the target that serves one installed application.
// The id's shape is validated where an application is installed (see
// capabilities/apps); here only a blank id is refused.
func AppTarget(appID string) Target {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return Target{}
	}
	return Target{Kind: TargetApp, ID: appID}
}

// Valid reports whether the target names something.
func (t Target) Valid() bool {
	if t.ID == "" {
		return false
	}
	return t.Kind == TargetWorkspace || t.Kind == TargetApp
}

// Key is the pool's map key. The kind is part of it and the separator is
// a byte no path or app id can contain, so the two scopes can never
// collide on their IDs.
func (t Target) Key() string {
	return string(t.Kind) + "\x00" + t.ID
}

// String renders the target for logs and error text.
func (t Target) String() string {
	if !t.Valid() {
		return "target=<none>"
	}
	if t.Kind == TargetApp {
		return "app=" + t.ID
	}
	return "workspace=" + t.ID
}
