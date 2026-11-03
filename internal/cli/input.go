package cli

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"
	"tdev/internal/protocol"
)

type directoryCompleter func(context.Context, string) ([]string, error)
type completionResult struct {
	id      int
	matches []string
	err     error
}
type promptModel struct {
	input                 textinput.Model
	label                 string
	complete              directoryCompleter
	ctx                   context.Context
	queryCancel           context.CancelFunc
	queryID               int
	matches               []string
	selected              int
	width, height         int
	busy, done, cancelled bool
	notice                string
}

func (m *promptModel) Init() tea.Cmd { return textinput.Blink }
func (m *promptModel) clearCompletion() {
	if m.queryCancel != nil {
		m.queryCancel()
		m.queryCancel = nil
	}
	m.queryID++
	m.busy = false
	m.matches = nil
	m.selected = -1
	m.notice = ""
}
func commonDirectoryPrefix(matches []string) string {
	if len(matches) == 0 {
		return ""
	}
	prefix := []rune(matches[0])
	for _, match := range matches[1:] {
		runes := []rune(match)
		i := 0
		for i < len(prefix) && i < len(runes) && prefix[i] == runes[i] {
			i++
		}
		prefix = prefix[:i]
	}
	return string(prefix)
}
func (m *promptModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(8, msg.Width), max(4, msg.Height)
		m.input.SetWidth(max(1, m.width-4))
		m.input.SetCursor(m.input.Position())
		return m, nil
	case tea.PasteMsg:
		m.clearCompletion()
	case completionResult:
		if msg.id != m.queryID {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.notice = "补全失败，可继续输入或重试 Tab"
			return m, nil
		}
		if len(msg.matches) == 0 {
			m.notice = "没有匹配目录"
			return m, nil
		}
		value := m.input.Value()
		if prefix := commonDirectoryPrefix(msg.matches); strings.HasPrefix(prefix, value) && len(prefix) > len(value) {
			m.input.SetValue(prefix)
			m.input.CursorEnd()
		}
		if len(msg.matches) > 1 {
			m.matches = msg.matches
			m.selected = -1
		}
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		switch key {
		case "ctrl+c":
			m.clearCompletion()
			m.cancelled = true
			m.done = true
			return m, tea.Quit
		case "ctrl+d":
			if m.input.Value() == "" {
				m.cancelled = true
				m.done = true
				return m, tea.Quit
			}
		case "ctrl+z":
			return m, nil
		case "enter":
			if m.selected >= 0 && m.selected < len(m.matches) {
				m.input.SetValue(m.matches[m.selected])
				m.input.CursorEnd()
				m.clearCompletion()
				return m, nil
			}
			m.clearCompletion()
			m.done = true
			return m, tea.Quit
		case "esc":
			m.clearCompletion()
			return m, nil
		case "tab", "shift+tab", "up", "down", "left", "right":
			if len(m.matches) > 0 && (key != "left" && key != "right" || m.selected >= 0) {
				delta := 1
				if key == "up" || key == "left" || key == "shift+tab" {
					delta = -1
				}
				if m.selected < 0 && delta < 0 {
					m.selected = 0
				}
				m.selected = (m.selected + delta + len(m.matches)) % len(m.matches)
				return m, nil
			}
			if key == "tab" {
				if m.complete == nil || m.input.Position() != len([]rune(m.input.Value())) {
					return m, nil
				}
				m.clearCompletion()
				m.busy = true
				ctx, cancel := context.WithCancel(m.ctx)
				m.queryCancel = cancel
				id, prefix, complete := m.queryID, m.input.Value(), m.complete
				return m, func() tea.Msg { matches, err := complete(ctx, prefix); return completionResult{id, matches, err} }
			}
		}
		// Typed edits invalidate displayed and in-flight suggestions.
		m.clearCompletion()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}
func (m *promptModel) View() tea.View {
	if m.done {
		return tea.NewView("")
	}
	// Separate the label from the scrolling input to keep CJK prompts in bounds.
	var view strings.Builder
	view.WriteString(ansi.Truncate(m.label, m.width-1, "…") + "\n" + m.input.View())
	if m.busy {
		view.WriteString("\n" + ansi.Truncate("正在查询远端目录…", m.width-1, "…"))
	}
	if m.notice != "" {
		view.WriteString("\n" + ansi.Truncate(m.notice, m.width-1, "…"))
	}
	if len(m.matches) > 0 {
		rows := min(7, max(1, m.height-4))
		start := max(0, m.selected-rows+1)
		end := min(len(m.matches), start+rows)
		for i := start; i < end; i++ {
			marker := "  "
			if i == m.selected {
				marker = "> "
			}
			name := path.Base(strings.TrimSuffix(m.matches[i], "/")) + "/"
			view.WriteString("\n" + marker + ansi.Truncate(name, m.width-3, "…"))
		}
		view.WriteString("\n" + ansi.Truncate(fmt.Sprintf("%d 个目录 · Tab/方向键选择，Enter 确认", len(m.matches)), m.width-1, "…"))
	}
	return tea.NewView(view.String())
}
func (a *app) editLine(ctx context.Context, label, fallback string, complete directoryCompleter) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	input := textinput.New()
	input.SetVirtualCursor(true)
	input.Prompt = "> "
	input.Placeholder = fallback
	input.CharLimit = 4096
	input.Focus()
	// Terminal paste remains available; system clipboard access stays explicit.
	input.KeyMap.Paste.SetEnabled(false)
	width, height, err := term.GetSize(int(a.in.(*os.File).Fd()))
	if err != nil {
		width, height = 80, 24
	}
	if fallback != "" {
		label += " [" + fallback + "]"
	}
	model := &promptModel{input: input, label: label, complete: complete, ctx: ctx, width: max(8, width), height: max(4, height), selected: -1}
	model.input.SetWidth(max(1, model.width-4))
	program := tea.NewProgram(model, tea.WithInput(a.in), tea.WithOutput(a.errOut), tea.WithContext(ctx), tea.WithoutSignalHandler())
	_, err = program.Run()
	model.clearCompletion()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", err
	}
	if model.cancelled {
		return "", protocol.Fail("CANCELLED", 130, "input cancelled")
	}
	value := strings.TrimSpace(model.input.Value())
	if value == "" {
		value = fallback
	}
	return value, nil
}
