package tui

import "github.com/charmbracelet/lipgloss"

// startupBanner renders the opencraft brand block shown once when the
// TUI starts: the wordmark logo on the left, a long vertical divider,
// and the startup status (version, model, workdir, session id) on the
// right. It becomes the first entry of the in-memory transcript, so
// it stays reachable in the scrollable viewport as the conversation
// proceeds instead of scrolling away into the terminal scrollback.
func startupBanner(opts Options, version string) []string {
	logo := opencraftLogo()
	logoWidth := 0
	for _, r := range logo {
		if w := lipgloss.Width(r); w > logoWidth {
			logoWidth = w
		}
	}

	model := opts.Model
	if model == "" {
		model = "unset"
	}
	workdir := displayPath(opts.WorkDir)
	if workdir == "" {
		workdir = "—"
	}
	session := opts.ContextID
	if session == "" {
		session = "—"
	}

	info := []string{}
	if version != "" {
		info = append(info, dimStyle.Render("v"+version))
	}
	info = append(info,
		"model:   "+statusTextStyle.Render(model),
		"workdir: "+dimStyle.Render(workdir),
		"session: "+dimStyle.Render(session),
	)

	height := max(len(logo), len(info))
	for len(info) < height {
		info = append(info, "")
	}
	lines := make([]string, 0, height)
	for i := range height {
		left := lipgloss.NewStyle().Width(logoWidth).Render("")
		switch i {
		case 0:
			left = identityStyle.Render(logo[0])
		case 1:
			left = statusTextStyle.Render(logo[1])
		}
		lines = append(lines,
			left+"  │  "+info[i])
	}
	return lines
}

// opencraftLogo is the startup brand: the name itself, with a flow
// wave beneath it. Same trick as the pi logo — the mark is the name —
// plus the flowcraft in-joke: the word sits on its own waterline.
func opencraftLogo() []string {
	return []string{
		"OpenCraft",
		"∿∿∿∿∿∿∿∿∿",
	}
}
