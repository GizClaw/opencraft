// Package procmem reads the memory footprint of this process family: the
// host process, everything it spawns (sandbox executables, MCP servers,
// plugin binaries) and the platform processes that run on its behalf while
// being reparented to launchd (on macOS WKWebView's WebContent, GPU and
// Networking processes).
//
// It is the reading half of the desktop Diagnostics charts: the runtime
// metric sampler in internal/adapters/desktop records one snapshot per
// tick, and the family total is what a growing window or a leaked document
// shows up in. Both platform probes use the OS's own process APIs (libproc
// on macOS, /proc on Linux) and need no elevated permissions, because every
// member of the family runs as this user.
//
// It depends on nothing but the standard library.
package procmem

import (
	"errors"
	"sort"
	"strings"
)

// ErrUnsupported reports that this platform has no probe. Callers keep
// running without the series instead of failing the sampler.
var ErrUnsupported = errors.New("procmem: unsupported platform")

// Role classifies one member of the family. The first three platform roles
// are what the process name says the process is; the rest is structure.
type Role string

const (
	// RoleSelf is the host process itself.
	RoleSelf Role = "self"
	// RoleChild is a process this app spawned, at any depth: an execd
	// sandbox child, an MCP server, a plugin binary, a command they ran.
	RoleChild Role = "child"
	// RoleWebContent is a web renderer process (macOS WebKit's
	// com.apple.WebKit.WebContent, Linux's WebKitWebProcess).
	RoleWebContent Role = "webcontent"
	// RoleGPU is a web compositor process.
	RoleGPU Role = "gpu"
	// RoleNetworking is a web networking process.
	RoleNetworking Role = "networking"
	// RoleHelper is any other process the platform reports as owned by
	// this app, e.g. the audio and updater XPC services.
	RoleHelper Role = "helper"
)

// Sources of family membership, reported as a snapshot attribute so a
// chart can say why the renderers are (or are not) in the total.
const (
	// SourceResponsibility means the platform reports process ownership
	// and the snapshot used it: helpers that launchd reparented to pid 1
	// (WebKit's processes) are included.
	SourceResponsibility = "responsibility"
	// SourceTree means membership came from the parent/child tree alone,
	// so only what this app spawned itself is in the total.
	SourceTree = "tree"
)

// Process is one member of the family.
type Process struct {
	PID  int
	PPID int
	// Name is the executable's base name as the platform reports it.
	Name string
	// Path is the executable's full path when the platform can report it.
	Path string
	Role Role
	// Footprint is the bytes the process holds usable: phys_footprint on
	// macOS (what Activity Monitor shows), Pss on Linux.
	Footprint uint64
	// Resident is the bytes resident in physical memory.
	Resident uint64
}

// Family is one snapshot of the process family rooted at Root, ordered by
// pid so two snapshots of the same family line up.
type Family struct {
	Root    int
	Source  string
	Members []Process
}

// Total sums the family footprints in bytes.
func (f Family) Total() uint64 {
	var total uint64
	for _, member := range f.Members {
		total += member.Footprint
	}
	return total
}

// Member returns the family member with this pid.
func (f Family) Member(pid int) (Process, bool) {
	for _, member := range f.Members {
		if member.PID == pid {
			return member, true
		}
	}
	return Process{}, false
}

// record is one process as a platform probe found it, before membership
// and roles are resolved.
type record struct {
	pid  int
	ppid int
	name string
}

// assemble is the platform-independent half of a snapshot. It takes every
// process the platform reported together with the owners the platform's
// ownership API named (pid -> the pid it belongs to; nil when the platform
// has no such API) and returns the family: this process, everything
// descending from it, and everything it is reported to own even though
// launchd is the parent of record.
func assemble(root int, records []record, owners map[int]int) Family {
	byPID := make(map[int]record, len(records))
	for _, rec := range records {
		byPID[rec.pid] = rec
	}
	descends := func(pid int) bool {
		// The ppid chain is short; the bound only guards a broken tree
		// (a pid reused while walking) from looping.
		for steps := 0; pid > 0 && steps <= len(records); steps++ {
			if pid == root {
				return true
			}
			parent, ok := byPID[pid]
			if !ok {
				return false
			}
			pid = parent.ppid
		}
		return false
	}
	family := Family{Root: root, Source: SourceTree}
	if owners != nil {
		family.Source = SourceResponsibility
	}
	for _, rec := range records {
		self := rec.pid == root
		descendant := !self && descends(rec.pid)
		mine := descendant
		if !self && !descendant && owners != nil && owners[rec.pid] == root {
			mine = true
		}
		if !self && !mine {
			continue
		}
		role := RoleSelf
		if !self {
			role = roleFor(rec.name, descendant)
		}
		family.Members = append(family.Members, Process{
			PID:  rec.pid,
			PPID: rec.ppid,
			Name: rec.name,
			Role: role,
		})
	}
	sort.Slice(family.Members, func(i, j int) bool {
		return family.Members[i].PID < family.Members[j].PID
	})
	return family
}

// roleFor assigns a role to one member: a process in this app's pid tree
// is a child, anything else is a platform helper classified by name. The
// names differ per platform (com.apple.WebKit.WebContent, WebKitWebProcess,
// WebKitNetworkProcess), so the match is on the lowercase substrings they
// share. It is only ever applied to members, and the helper set is small
// and platform-owned, so a loose match cannot pick up a user program.
func roleFor(name string, descendant bool) Role {
	if descendant {
		return RoleChild
	}
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "webcontent"), strings.Contains(lower, "webkitwebprocess"):
		return RoleWebContent
	case strings.Contains(lower, "gpu"):
		return RoleGPU
	case strings.Contains(lower, "networking"), strings.Contains(lower, "networkprocess"):
		return RoleNetworking
	default:
		return RoleHelper
	}
}
