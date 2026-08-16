package tui

import tea "github.com/charmbracelet/bubbletea"

// slashHandlers maps command names (defined in the commands
// subpackage) to their Model execution. It lives here because the
// handlers need the TUI Model, which the subpackage cannot import.
var slashHandlers = map[string]func(m *Model) (tea.Model, tea.Cmd){
	"resume": func(m *Model) (tea.Model, tea.Cmd) {
		return m.enterResumeMode(), nil
	},
	"permissions": func(m *Model) (tea.Model, tea.Cmd) {
		return m.enterPermissionsMode(), nil
	},
}

// runCommand executes a slash command by name and returns the model
// and command it produced.
func (m *Model) runCommand(name string) (tea.Model, tea.Cmd) {
	if h := slashHandlers[name]; h != nil {
		return h(m)
	}
	// Defensive: paletteSelection only yields names from the command
	// corpus, so a miss here means the corpus and slashHandlers have
	// drifted apart (guarded by TestSlashHandlersMatchCorpus). Surface
	// it instead of silently doing nothing.
	m.queue(dimStyle.Render("unknown command: /" + name))
	return m, m.flushPending()
}
