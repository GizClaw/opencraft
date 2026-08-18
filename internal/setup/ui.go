// The interactive first-run wizard: a small bubbletea program that
// walks through provider selection / keys / Azure endpoint, then writes
// the generated user configuration layer and exits back into the app.
package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type step int

const (
	stepWelcome step = iota
	stepProvider
	stepProviderOrder
	stepProviderConfig
	stepConfirm
	stepDone
)

// phase is the per-provider sub-step inside stepProviderConfig.
type phase int

const (
	phaseKeySource phase = iota
	phaseKeyInput
	phaseEndpoint // azure
	phaseModel    // azure deployment name
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	checkStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
)

type model struct {
	configDir   string
	step        step
	selected    []bool
	keys        []KeyedProvider // parallel to Providers; only selected filled
	order       []int           // selected provider indices, priority order
	orderCursor int
	cursor      int
	providerIdx int
	orderPos    int
	phase       phase
	keySource   int
	errMsg      string
	fileExists  bool

	keyInput      textinput.Model
	endpointInput textinput.Model
	modelInput    textinput.Model
}

func newModel(configDir string) model {
	m := model{configDir: configDir}
	m.selected = make([]bool, len(Providers))
	m.keys = make([]KeyedProvider, len(Providers))
	for i, p := range Providers {
		m.keys[i] = KeyedProvider{Provider: p}
		if os.Getenv(p.EnvVar) != "" {
			m.selected[i] = true
			m.keys[i].KeySource = KeyEnv
		} else {
			m.keys[i].KeySource = KeyLiteral
		}
	}

	m.keyInput = textinput.New()
	m.keyInput.Prompt = "> "
	m.keyInput.Placeholder = "sk-..."
	m.keyInput.CharLimit = 512
	m.keyInput.Width = 60
	m.keyInput.EchoMode = textinput.EchoPassword

	m.endpointInput = textinput.New()
	m.endpointInput.Prompt = "> "
	m.endpointInput.Placeholder = "https://my-resource.openai.azure.com"
	m.endpointInput.CharLimit = 256
	m.endpointInput.Width = 60

	m.modelInput = textinput.New()
	m.modelInput.Prompt = "> "
	m.modelInput.Placeholder = "deployment name"
	m.modelInput.CharLimit = 128
	m.modelInput.Width = 48

	if _, err := os.Stat(filepath.Join(configDir, "opencraft.yaml")); err == nil {
		m.fileExists = true
	}
	return m
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.step > stepWelcome && m.step != stepDone {
			m.step = m.backStep()
			return m, nil
		}
	}

	switch m.step {
	case stepWelcome:
		if key.String() == "enter" {
			m.step = stepProvider
		}

	case stepProvider:
		switch key.String() {
		case "up", "k":
			m.cursor = (m.cursor + len(Providers) - 1) % len(Providers)
		case "down", "j":
			m.cursor = (m.cursor + 1) % len(Providers)
		case " ":
			m.selected[m.cursor] = !m.selected[m.cursor]
		case "enter":
			m.beginOrder()
		}

	case stepProviderOrder:
		switch key.String() {
		case "up", "k":
			m.orderCursor = (m.orderCursor + len(m.order) - 1) % len(m.order)
		case "down", "j":
			m.orderCursor = (m.orderCursor + 1) % len(m.order)
		case "u":
			m.moveOrder(-1)
		case "d":
			m.moveOrder(+1)
		case "enter":
			m.beginProviderConfig()
		}

	case stepProviderConfig:
		var cmd tea.Cmd
		switch m.phase {
		case phaseKeySource:
			switch key.String() {
			case "up", "k", "down", "j":
				m.keySource = (m.keySource + 1) % 2
			case "enter":
				if m.keySource == 0 {
					m.keys[m.providerIdx].KeySource = KeyEnv
					m.advanceProvider()
				} else {
					m.keys[m.providerIdx].KeySource = KeyLiteral
					m.keys[m.providerIdx].KeyValue = ""
					m.keyInput.SetValue("")
					m.keyInput.Focus()
					m.phase = phaseKeyInput
				}
			}
		case phaseKeyInput:
			m.keyInput, cmd = m.keyInput.Update(msg)
			if key.String() == "enter" {
				value := strings.TrimSpace(m.keyInput.Value())
				if value == "" {
					m.errMsg = "API key 不能为空"
					return m, nil
				}
				m.errMsg = ""
				m.keys[m.providerIdx].KeyValue = value
				m.advanceProvider()
			}
		case phaseEndpoint:
			m.endpointInput, cmd = m.endpointInput.Update(msg)
			if key.String() == "enter" {
				value := strings.TrimSpace(m.endpointInput.Value())
				if value == "" {
					m.errMsg = "Azure endpoint 不能为空（https://...openai.azure.com）"
					return m, nil
				}
				m.errMsg = ""
				m.keys[m.providerIdx].Endpoint = value
				m.phase = phaseModel
				m.modelInput.SetValue(m.keys[m.providerIdx].Model)
				m.modelInput.Focus()
			}
		case phaseModel:
			m.modelInput, cmd = m.modelInput.Update(msg)
			if key.String() == "enter" {
				value := strings.TrimSpace(m.modelInput.Value())
				if value == "" {
					m.errMsg = "Azure deployment 名称不能为空"
					return m, nil
				}
				m.errMsg = ""
				m.keys[m.providerIdx].Model = value
				m.phase = phaseKeySource
				m.keySource = defaultKeySource(m.keys[m.providerIdx].Provider)
			}
		}
		return m, cmd

	case stepConfirm:
		if key.String() == "enter" {
			cfg := m.config()
			if err := cfg.Write(m.configDir); err != nil {
				m.errMsg = err.Error()
			} else {
				m.errMsg = ""
				m.step = stepDone
			}
		}

	case stepDone:
		if key.String() == "enter" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *model) beginProviderConfig() {
	m.orderPos = -1
	m.advanceProvider()
	if m.providerIdx >= 0 {
		m.step = stepProviderConfig
	}
}

// beginOrder snapshots the selected providers into the priority list
// (catalog order) and shows the ordering step.
func (m *model) beginOrder() {
	m.order = m.order[:0]
	for i, selected := range m.selected {
		if selected {
			m.order = append(m.order, i)
		}
	}
	if len(m.order) == 0 {
		return
	}
	m.orderCursor = 0
	m.step = stepProviderOrder
}

// moveOrder shifts the item under the cursor by delta within the
// priority list (clamped at the ends).
func (m *model) moveOrder(delta int) {
	if len(m.order) < 2 {
		return
	}
	target := m.orderCursor + delta
	if target < 0 || target >= len(m.order) {
		return
	}
	m.order[m.orderCursor], m.order[target] = m.order[target], m.order[m.orderCursor]
	m.orderCursor = target
}

func (m *model) advanceProvider() {
	for pos := m.orderPos + 1; pos < len(m.order); pos++ {
		m.orderPos = pos
		m.providerIdx = m.order[pos]
		m.prepareProvider()
		return
	}
	m.step = stepConfirm
}

func (m *model) prepareProvider() {
	p := Providers[m.providerIdx]
	if p.Azure {
		m.endpointInput.SetValue(m.keys[m.providerIdx].Endpoint)
		m.endpointInput.Focus()
		m.phase = phaseEndpoint
		return
	}
	m.keySource = defaultKeySource(p)
	m.phase = phaseKeySource
}

func defaultKeySource(p Provider) int {
	if os.Getenv(p.EnvVar) != "" {
		return 0 // env
	}
	return 1 // literal
}

func (m model) backStep() step {
	switch m.step {
	case stepProvider:
		return stepWelcome
	case stepProviderOrder:
		return stepProvider
	case stepProviderConfig:
		return stepProviderOrder
	case stepConfirm:
		return stepProviderOrder
	}
	return stepWelcome
}

// config assembles the final Config from the selected providers.
func (m model) config() Config {
	var cfg Config
	for _, i := range m.order {
		cfg.Providers = append(cfg.Providers, m.keys[i])
	}
	return cfg
}

func (m model) View() string {
	switch m.step {
	case stepWelcome:
		return m.viewWelcome()
	case stepProvider:
		return m.viewProvider()
	case stepProviderOrder:
		return m.viewProviderOrder()
	case stepProviderConfig:
		return m.viewProviderConfig()
	case stepConfirm:
		return m.viewConfirm()
	case stepDone:
		return m.viewDone()
	}
	return ""
}

func (m model) viewWelcome() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("opencraft 首次配置"))
	b.WriteString("\n\n")
	b.WriteString("所有 provider 都会注册，你只需要勾选自己有的 key。\n")
	b.WriteString("向导生成 ")
	b.WriteString(dimStyle.Render("~/.opencraft/config/opencraft.yaml"))
	b.WriteString("，之后所有配置都写在这个文件里。\n")
	b.WriteString("router 会按优先级自动选择，失败的 provider 自动顺延到下一个；\n")
	b.WriteString("勾选后还可以调整优先级顺序。\n")
	if m.fileExists {
		b.WriteString("\n" + errStyle.Render("注意：已存在 opencraft.yaml，确认后将整文件覆盖。") + "\n")
	}
	b.WriteString("\n" + dimStyle.Render("Enter 开始 · Ctrl+C 退出"))
	return b.String()
}

func (m model) viewProvider() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("选择你有 key 的 provider（可多选）"))
	b.WriteString("\n\n")
	for i, p := range Providers {
		marker := "  "
		if i == m.cursor {
			marker = cursorStyle.Render("▸") + " "
		}
		state := dimStyle.Render("[ ]")
		if m.selected[i] {
			state = checkStyle.Render("[✓]")
		}
		name := p.Name
		if os.Getenv(p.EnvVar) != "" {
			name += " " + okStyle.Render("(env ✓)")
		}
		model := p.DefaultModel
		if p.Azure {
			model = "endpoint + deployment"
		}
		b.WriteString(fmt.Sprintf("%s %s %-24s %-32s %s\n",
			marker, state, name, model, dimStyle.Render(p.EnvVar)))
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ 或 j/k 移动 · Space 选择 · Enter 调整优先级 · Esc 返回"))
	return b.String()
}

func (m model) viewProviderOrder() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("调整 router 优先级（从上到下）"))
	b.WriteString("\n\n")
	b.WriteString("第一个是主模型，失败后按顺序顺延。\n\n")
	for i, idx := range m.order {
		p := Providers[idx]
		marker := "  "
		if i == m.orderCursor {
			marker = cursorStyle.Render("▸") + " "
		}
		model := p.DefaultModel
		if p.Azure {
			model = m.keys[idx].Model
			if model == "" {
				model = "endpoint + deployment"
			}
		}
		b.WriteString(fmt.Sprintf("%s %d. %-20s %s\n",
			marker, i+1, p.Name, dimStyle.Render(model)))
	}
	b.WriteString("\n" + dimStyle.Render("↑/↓ 或 j/k 移动 · u/d 上移/下移 · Enter 继续 · Esc 返回"))
	return b.String()
}

func (m model) viewProviderConfig() string {
	p := Providers[m.providerIdx]
	index := m.orderPos + 1
	total := len(m.order)
	header := fmt.Sprintf("%s（%d/%d）", p.Name, index, total)
	switch m.phase {
	case phaseEndpoint:
		return m.viewInput("Azure endpoint · "+header, "Azure OpenAI 资源 URL", m.endpointInput)
	case phaseModel:
		return m.viewInput("Azure deployment · "+header, "部署名称（就是模型 ID）", m.modelInput)
	case phaseKeySource:
		return m.viewKeySource(header)
	case phaseKeyInput:
		return m.viewInput("API key · "+header,
			"密钥将以明文写入 ~/.opencraft/config/opencraft.yaml（0600）", m.keyInput)
	}
	return ""
}

func (m model) viewInput(title, hint string, input textinput.Model) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")
	b.WriteString(hint)
	b.WriteString("\n")
	b.WriteString(input.View())
	if m.errMsg != "" {
		b.WriteString("\n\n" + errStyle.Render(m.errMsg))
	}
	b.WriteString("\n\n" + dimStyle.Render("Enter 确认 · Esc 返回"))
	return b.String()
}

func (m model) viewKeySource(header string) string {
	p := Providers[m.providerIdx]
	env := p.EnvVar
	set := os.Getenv(env) != ""
	rows := []struct {
		label string
		desc  string
	}{
		{label: "环境变量", desc: "${env:" + env + "}" + envStatus(set)},
		{label: "直接输入", desc: "明文写入 opencraft.yaml（0600）"},
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("API key · " + header))
	b.WriteString("\n\n")
	for i, r := range rows {
		marker := "  "
		if i == m.keySource {
			marker = cursorStyle.Render("▸") + " "
		}
		b.WriteString(fmt.Sprintf("%s %-8s %s\n", marker, r.label, dimStyle.Render(r.desc)))
	}
	if m.errMsg != "" {
		b.WriteString("\n" + errStyle.Render(m.errMsg))
	}
	b.WriteString("\n\n" + dimStyle.Render("↑/↓ 或 j/k 选择 · Enter 确认 · Esc 返回"))
	return b.String()
}

func envStatus(set bool) string {
	if set {
		return "（已设置 ✓）"
	}
	return "（当前未设置）"
}

func (m model) viewConfirm() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("确认配置"))
	b.WriteString("\n\n")
	b.WriteString("已注册 provider：")
	for i, p := range Providers {
		if !p.Azure || m.selected[i] {
			b.WriteString(" " + p.ID)
		}
	}
	b.WriteString("\n\n")
	b.WriteString("router 优先级（key 已配置）：\n")
	for i, k := range m.config().Providers {
		keyDesc := "${env:" + k.Provider.EnvVar + "}"
		if k.KeySource == KeyLiteral {
			keyDesc = "明文"
		}
		model := k.Model
		if model == "" {
			model = k.Provider.DefaultModel
		}
		extra := ""
		if k.Provider.Azure {
			extra = fmt.Sprintf("  endpoint=%s", k.Endpoint)
		}
		fmt.Fprintf(&b, "  %d. %-12s %-20s %s%s\n",
			i+1, k.Provider.ID, model, keyDesc, extra)
	}
	b.WriteString("\n将写入 ")
	b.WriteString(dimStyle.Render("~/.opencraft/config/opencraft.yaml"))
	if m.fileExists {
		b.WriteString(" " + errStyle.Render("（覆盖现有文件）"))
	}
	if m.errMsg != "" {
		b.WriteString("\n\n" + errStyle.Render(m.errMsg))
	}
	b.WriteString("\n\n" + dimStyle.Render("Enter 写入并完成 · Esc 返回修改 · Ctrl+C 退出"))
	return b.String()
}

func (m model) viewDone() string {
	var b strings.Builder
	b.WriteString(okStyle.Render("配置完成"))
	b.WriteString("\n\n已写入：\n")
	b.WriteString("  " + dimStyle.Render("~/.opencraft/config/opencraft.yaml") + "\n")
	b.WriteString("\n" + dimStyle.Render("Enter 启动 opencraft"))
	return b.String()
}

// Run starts the first-run configuration wizard and blocks until it
// finishes. A non-nil error reports a write failure or a fatal UI
// error.
func Run(configDir string) error {
	m := newModel(configDir)
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(model); ok && fm.errMsg != "" {
		return errors.New(fm.errMsg)
	}
	return nil
}
