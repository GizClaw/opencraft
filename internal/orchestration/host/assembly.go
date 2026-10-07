package host

import "time"

// AssemblyAttempt is one assembly the pool ran for a target: why it ran,
// when it ended, and what it failed with.
type AssemblyAttempt struct {
	// Reason is what asked for the assembly (ReasonAppTurn,
	// ReasonAppEnable, ReasonSettingsSave, ReasonWorkspaceOpen, ...),
	// or ReasonUnknown for a caller that tagged nothing.
	Reason AssemblyReason
	// At is when the attempt ended, in UTC.
	At time.Time
	// Err is the refusal as the assembly reported it, empty when the
	// attempt succeeded. It is the text and not the error because its
	// reader is a person with a YAML file open — an application's
	// assembly failures are document-validation refusals, and the log
	// line is the only other copy.
	Err string
}

// Failed reports whether this attempt was refused.
func (a AssemblyAttempt) Failed() bool { return a.Err != "" }

// AssemblyStats is one target's assembly history in this process. It is
// the diagnostics view of a life cycle that otherwise exists only as log
// lines: how many times the runtime was assembled, why the last attempt
// ran, and — separately, because a fixed document does not stop being
// worth reading — why the last failure happened.
//
// The zero value is "the pool has not assembled this target yet", which
// a page shows as such rather than as a count of zero assemblies.
type AssemblyStats struct {
	// Count is how many assemblies completed: the number the
	// assembly_seq log line reports.
	Count int
	// Last is the most recent attempt, successful or not.
	Last AssemblyAttempt
	// LastFailure is the most recent attempt that was refused. A later
	// success leaves it standing: "it works now" and "this is what was
	// wrong" are different answers, and a page that has just started
	// serving again is exactly when someone opens the panel to read the
	// second one.
	LastFailure AssemblyAttempt
}

// AssemblyStats returns what the pool remembers about one target's
// assemblies in this process.
func (m *Manager) AssemblyStats(t Target) AssemblyStats {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stats := m.assemblies[t.Key()]; stats != nil {
		return *stats
	}
	return AssemblyStats{}
}

// recordAssembly folds one finished attempt into the target's record and
// returns the record as it then stood.
func (m *Manager) recordAssembly(
	t Target,
	reason AssemblyReason,
	at time.Time,
	err error,
) AssemblyStats {
	attempt := AssemblyAttempt{Reason: reason, At: at}
	if err != nil {
		attempt.Err = err.Error()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.assemblies == nil {
		m.assemblies = make(map[string]*AssemblyStats)
	}
	stats := m.assemblies[t.Key()]
	if stats == nil {
		stats = &AssemblyStats{}
		m.assemblies[t.Key()] = stats
	}
	stats.Last = attempt
	if attempt.Failed() {
		stats.LastFailure = attempt
	} else {
		stats.Count++
	}
	return *stats
}
