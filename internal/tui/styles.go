package tui

import "github.com/charmbracelet/lipgloss"

// The palette sticks to the 16 semantic ANSI colors (no 256-color
// indexes and no RGB), following the Codex-style CLI guidelines,
// except the composer bar, which uses 256-level grays so the box
// reads as opaque and its white text stays crisp:
//
//	bright cyan    - user replies, selections, status
//	bright green   - success / additions
//	bright red     - failure / deletions
//	bright magenta - opencraft agent identity
//	bright black   - dim labels, tool calls, and chrome
//
// Assistant body text keeps the terminal's default foreground.
var (
	identityStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")).
			Bold(true)

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14")).
			Bold(true)

	reasoningLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Bold(true)

	// reasoningPanelStyle is the fixed panel above the composer
	// showing the live reasoning tail: a transparent body with a gray
	// rounded frame, so it stays distinct from the gray composer
	// without tinting the terminal background.
	reasoningPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("8")).
				Padding(0, 1)

	// reasoningPanelText is the dim italic reasoning text inside the
	// panel.
	reasoningPanelText = lipgloss.NewStyle().
				Foreground(lipgloss.Color("8")).
				Italic(true)

	// assistantRuleStyle is the white rule framing an assistant
	// message block, matching the reasoning box's white border.
	assistantRuleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("15"))

	toolNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13")).
			Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))

	toolOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10"))

	toolErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9"))

	statusTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("14"))

	// composerBG is the solid gray behind the input bar, picked from
	// the 256 grays so it reads as opaque instead of washing into the
	// terminal background. composerText is white on dark terminals and
	// black on light ones so the text stays readable either way.
	composerBG   = lipgloss.AdaptiveColor{Light: "250", Dark: "238"}
	composerText = lipgloss.AdaptiveColor{Light: "0", Dark: "15"}

	// inputBoxStyle is the gray composer bar around the prompt; it
	// grows with the input and spans the full terminal width. One
	// cell of padding on every side keeps the content clear of the
	// bar's edges.
	inputBoxStyle = lipgloss.NewStyle().
			Background(composerBG).
			Padding(1, 1)

	// inputTextStyle is the typed/echoed text inside the composer.
	inputTextStyle = lipgloss.NewStyle().
			Foreground(composerText).
			Background(composerBG)

	// composerPromptStyle is the "> " prompt inside the bar.
	composerPromptStyle = lipgloss.NewStyle().
				Foreground(composerText).
				Bold(true).
				Background(composerBG)

	// composerPlaceholderStyle is the hint text shown when the input
	// is empty; same white-on-gray as typed text.
	composerPlaceholderStyle = lipgloss.NewStyle().
					Foreground(composerText).
					Background(composerBG)

	spinnerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("13"))
)
