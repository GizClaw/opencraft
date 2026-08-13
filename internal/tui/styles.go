package tui

import "github.com/charmbracelet/lipgloss"

var (
	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderBottom(true).
			BorderForeground(lipgloss.Color("240"))

	userStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("39")).
			Bold(true)

	assistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	reasoningStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Italic(true)

	toolTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("214")).
			Bold(true)

	toolOKStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("78"))

	toolErrStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Bold(true)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 1)

	inputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62"))
)
