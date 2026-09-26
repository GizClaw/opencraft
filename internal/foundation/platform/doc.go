// Package platform holds the host-OS primitives of foundation: the
// answers about the machine this process runs on, one question per
// subpackage.
//
// This directory is a namespace, not an abstraction. There is
// deliberately no Platform interface: each consumer needs one answer,
// a binary links exactly one implementation per GOOS, and the shape
// that is actually polymorphic (the sandbox execution surface, with
// capabilities carried as a value) already lives in flowcraft core.
// The rationale, and the entry conditions for ever revisiting it, are
// in docs/architecture-plan.md §W1.
//
// Admission rules, applied when reviewing a new platform primitive:
//
//   - Belongs here: an OS fact about this machine - pure functions or
//     table lookups that can be exercised for every GOOS from any
//     lane (shelldetect.Detect, envpath.Candidates) - or a side
//     effect that is itself the answer (listing installed fonts,
//     reading a sibling process's footprint). Platform branching is
//     either a build tag or a goos parameter.
//   - Does not belong here: a decision (which sandbox backend to run,
//     whether TTY sessions are offered - that is capabilities/sandbox),
//     a spawn performed on behalf of the UI (open, reveal, detach -
//     adapters/desktop/bindings; the argv table for such a spawn may
//     live here as a pure function), anything that knows about
//     workspaces, sessions, users or approvals (capabilities), and
//     platform bits that are not Go packages (Taskfile targets, cgo
//     or private-API build tags).
//   - Windows is either implemented or explicitly unsupported - a
//     typed error or a zero value plus a comment - never a silent
//     empty answer.
//
// "Platform" here means the host operating system. The app platform
// (user-installable applications and the capability surface they may
// declare) is a different concept: see docs/app-platform-plan.md.
package platform
