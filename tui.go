package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type uiMode int

const (
	modeNormal uiMode = iota
	modeInput
	modeConfirm
)

type actionType int

const (
	actionNone actionType = iota
	actionAttach
	actionCreate
	actionQuit
)

type action struct {
	typ     actionType
	session string
}

type detailMsg struct {
	gen  int
	text string
}

type scrollTickMsg struct {
	gen int
}

type model struct {
	sessions     []Session
	cursor       int
	width        int
	mode         uiMode
	input        textinput.Model
	inputMode    string // "new" or "rename"
	socket       string
	projectDir   string
	result       action
	errMsg       string
	detail       string
	detailGen    int
	detailOffset int
}

func newModel(sessions []Session, socket, projectDir string) model {
	ti := textinput.New()
	ti.CharLimit = 64
	ti.Prompt = ""

	cursor := 0
	for i, s := range sessions {
		if s.Attached == 0 {
			cursor = i
			break
		}
	}

	return model{
		sessions:   sessions,
		cursor:     cursor,
		socket:     socket,
		projectDir: projectDir,
		input:      ti,
		result:     action{typ: actionQuit},
	}
}

func (m model) Init() tea.Cmd {
	return m.triggerDetail()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil
	case detailMsg:
		if msg.gen != m.detailGen {
			return m, nil
		}
		m.detail = msg.text
		m.detailOffset = 0
		if m.detail != "" && len(m.detail) > m.detailWidth() {
			return m, scrollTickCmd(m.detailGen)
		}
		return m, nil
	case scrollTickMsg:
		if msg.gen != m.detailGen {
			return m, nil
		}
		if m.detailOffset+2 < len(m.detail)-m.detailWidth() {
			m.detailOffset += 2
			return m, scrollTickCmd(m.detailGen)
		}
		return m, nil
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.mode != modeNormal || msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	row := msg.Y - 2
	if row < 0 || row >= len(m.sessions) || m.sessions[row].Attached > 0 {
		return m, nil
	}
	m.cursor = row
	m.result = action{typ: actionAttach, session: m.sessions[row].Name}
	return m, tea.Quit
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeNormal:
		return m.handleNormalKey(msg)
	case modeInput:
		return m.handleInputKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m model) handleNormalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.errMsg = ""
	switch msg.String() {
	case "up", "k":
		for i := m.cursor - 1; i >= 0; i-- {
			if m.sessions[i].Attached == 0 {
				m.cursor = i
				break
			}
		}
		m.resetDetail()
		return m, m.triggerDetail()
	case "down", "j":
		for i := m.cursor + 1; i < len(m.sessions); i++ {
			if m.sessions[i].Attached == 0 {
				m.cursor = i
				break
			}
		}
		m.resetDetail()
		return m, m.triggerDetail()
	case "enter":
		if len(m.sessions) > 0 && m.sessions[m.cursor].Attached == 0 {
			m.result = action{typ: actionAttach, session: m.sessions[m.cursor].Name}
			return m, tea.Quit
		}
	case "n":
		m.mode = modeInput
		m.inputMode = "new"
		m.input.SetValue(nextSessionName(m.sessions))
		m.input.Focus()
		return m, textinput.Blink
	case "r":
		if len(m.sessions) > 0 && m.sessions[m.cursor].Attached == 0 {
			m.mode = modeInput
			m.inputMode = "rename"
			m.input.SetValue(m.sessions[m.cursor].Name)
			m.input.Focus()
			return m, textinput.Blink
		}
	case "d":
		if len(m.sessions) > 0 && m.sessions[m.cursor].Attached == 0 {
			m.mode = modeConfirm
		}
	case "q", "esc":
		m.result = action{typ: actionQuit}
		return m, tea.Quit
	}
	return m, nil
}

func (m model) handleInputKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		name := strings.TrimSpace(m.input.Value())
		if err := validSessionName(name); err != nil {
			m.errMsg = err.Error()
			m.mode = modeNormal
			m.input.Blur()
			return m, nil
		}
		for _, s := range m.sessions {
			if s.Name == name {
				m.errMsg = fmt.Sprintf("session %q already exists", name)
				m.mode = modeNormal
				m.input.Blur()
				return m, nil
			}
		}
		if m.inputMode == "new" {
			m.result = action{typ: actionCreate, session: name}
			return m, tea.Quit
		}
		oldName := m.sessions[m.cursor].Name
		if err := renameSession(m.socket, oldName, name); err != nil {
			m.errMsg = fmt.Sprintf("rename failed: %v", err)
			m.mode = modeNormal
			m.input.Blur()
			return m, nil
		}
		m.refreshSessions()
		m.mode = modeNormal
		m.input.Blur()
		return m, m.triggerDetail()
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		m.errMsg = ""
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y":
		name := m.sessions[m.cursor].Name
		if err := killSession(m.socket, name); err != nil {
			m.errMsg = fmt.Sprintf("delete failed: %v", err)
		} else {
			m.refreshSessions()
		}
		m.mode = modeNormal
		return m, m.triggerDetail()
	case "n", "esc":
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

func (m *model) refreshSessions() {
	all, err := listSessions(m.socket)
	if err != nil {
		m.errMsg = fmt.Sprintf("refresh failed: %v", err)
		return
	}
	m.sessions = all
	if m.cursor >= len(m.sessions) {
		m.cursor = max(0, len(m.sessions)-1)
	}
	if m.cursor < len(m.sessions) && m.sessions[m.cursor].Attached > 0 {
		for i := m.cursor; i < len(m.sessions); i++ {
			if m.sessions[i].Attached == 0 {
				m.cursor = i
				break
			}
		}
	}
	m.errMsg = ""
	m.resetDetail()
}

func (m *model) resetDetail() {
	m.detailGen++
	m.detail = ""
	m.detailOffset = 0
}

func (m model) triggerDetail() tea.Cmd {
	if len(m.sessions) == 0 || m.cursor >= len(m.sessions) {
		return nil
	}
	s := m.sessions[m.cursor]
	if s.TTY == "" {
		return nil
	}
	gen := m.detailGen
	tty := s.TTY
	return func() tea.Msg {
		return detailMsg{gen: gen, text: fetchDetail(tty)}
	}
}

func fetchDetail(tty string) string {
	ttyName := strings.TrimPrefix(tty, "/dev/")
	if ttyName == "" {
		return ""
	}
	output, err := exec.Command("ps", "-t", ttyName, "-o", "args=").Output()
	if err != nil {
		return ""
	}
	shells := map[string]bool{
		"zsh": true, "bash": true, "fish": true, "sh": true, "dash": true,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		base := filepath.Base(fields[0])
		base = strings.TrimPrefix(base, "-")
		if shells[base] {
			continue
		}
		return line
	}
	return ""
}

func scrollTickCmd(gen int) tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return scrollTickMsg{gen: gen}
	})
}

func (m model) detailWidth() int {
	if m.width > 4 {
		return m.width - 4
	}
	return 80
}

func (m model) renderDetailLine() string {
	w := m.detailWidth()
	if m.detail == "" || w <= 0 {
		return ""
	}
	if len(m.detail) <= w {
		return m.detail
	}
	end := m.detailOffset + w
	if end > len(m.detail) {
		end = len(m.detail)
	}
	return m.detail[m.detailOffset:end]
}

var (
	styleHeader      = lipgloss.NewStyle().Bold(true)
	styleFaint       = lipgloss.NewStyle().Faint(true)
	styleError       = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleSelected    = lipgloss.NewStyle().Background(lipgloss.Color("4")).Foreground(lipgloss.Color("15")).Bold(true)
	styleAttached    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleAttachedRow = lipgloss.NewStyle().Faint(true)
	styleDetail      = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
)

func (m model) View() string {
	var b strings.Builder

	header := styleHeader.Render("zed-tmux")
	path := styleFaint.Render(" · " + m.projectDir)
	b.WriteString("  " + header + path + "\n\n")

	switch m.mode {
	case modeNormal:
		m.viewNormal(&b)
	case modeInput:
		m.viewInput(&b)
	case modeConfirm:
		m.viewConfirm(&b)
	}

	return b.String()
}

func (m model) viewNormal(b *strings.Builder) {
	if len(m.sessions) == 0 {
		b.WriteString("  No sessions available\n\n")
	} else {
		for i, s := range m.sessions {
			cmd := s.CurrentCommand
			if cmd == "" {
				cmd = "?"
			}
			windows := ""
			if s.Windows > 1 {
				windows = fmt.Sprintf("  %dw", s.Windows)
			}
			idle := "idle " + formatIdle(s.Idle())

			if s.Attached > 0 {
				name := fmt.Sprintf("%-10s", s.Name)
				tag := styleAttached.Render("[attached]")
				row := fmt.Sprintf("    %s  %-12s%s  %s  %s", name, cmd, windows, idle, tag)
				b.WriteString(styleAttachedRow.Render(row) + "\n")
			} else if i == m.cursor {
				content := fmt.Sprintf("  ▸ %-10s  %-12s%s  %s", s.Name, cmd, windows, idle)
				if m.width > 1 {
					if pad := m.width - 1 - displayWidth(content); pad > 0 {
						content += strings.Repeat(" ", pad)
					}
				}
				b.WriteString(styleSelected.Render(content) + "\n")
			} else {
				b.WriteString(fmt.Sprintf("    %-10s  %-12s%s  %s\n",
					s.Name, cmd, windows, styleFaint.Render(idle)))
			}
		}
		b.WriteString("\n")
		if detail := m.renderDetailLine(); detail != "" {
			b.WriteString("  " + styleDetail.Render("⌘ "+detail) + "\n")
		} else {
			b.WriteString("\n")
		}
	}

	if m.errMsg != "" {
		b.WriteString("  " + styleError.Render(m.errMsg) + "\n\n")
	}

	help := "  ↑↓/click select  enter attach  n new  r rename  d delete  q quit"
	b.WriteString(styleFaint.Render(help))
}

func (m model) viewInput(b *strings.Builder) {
	label := "New session name"
	if m.inputMode == "rename" {
		label = "Rename session"
	}
	b.WriteString(fmt.Sprintf("  %s: %s\n\n", label, m.input.View()))

	if m.errMsg != "" {
		b.WriteString("  " + styleError.Render(m.errMsg) + "\n\n")
	}

	b.WriteString(styleFaint.Render("  enter confirm  esc cancel"))
}

func (m model) viewConfirm(b *strings.Builder) {
	name := m.sessions[m.cursor].Name
	b.WriteString(fmt.Sprintf("  Delete session %q? (y/n)\n", name))
}

func runTUI(sessions []Session, socket, projectDir string) (action, error) {
	m := newModel(sessions, socket, projectDir)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return action{}, fmt.Errorf("tui: %w", err)
	}
	return finalModel.(model).result, nil
}
