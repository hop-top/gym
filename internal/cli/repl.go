package cli

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type replModel struct {
	app     *App
	input   textinput.Model
	history []string
}

func newREPLModel(app *App) replModel {
	in := textinput.New()
	in.Prompt = "gym> "
	in.Placeholder = "type update | learn <name/url> | train [pattern] | paths | exit"
	in.Focus()
	return replModel{app: app, input: in, history: []string{"repl started", "type `exit` to quit"}}
}

func (m replModel) Init() tea.Cmd { return textinput.Blink }

func (m replModel) runLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	args := strings.Fields(line)
	oldOut := m.app.Stdout
	oldErr := m.app.Stderr
	var buf bytes.Buffer
	m.app.Stdout = &buf
	m.app.Stderr = &buf
	err := m.app.exec(args)
	m.app.Stdout = oldOut
	m.app.Stderr = oldErr

	out := strings.TrimSpace(buf.String())
	if err != nil {
		if out != "" {
			return out + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	if out == "" {
		return "ok"
	}
	return out
}

func (m replModel) appendHistory(lines ...string) replModel {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			m.history = append(m.history, l)
		}
	}
	if len(m.history) > 20 {
		m.history = m.history[len(m.history)-20:]
	}
	return m
}

func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			line := m.input.Value()
			trimmed := strings.TrimSpace(line)
			if trimmed == "exit" || trimmed == "quit" {
				return m, tea.Quit
			}
			if trimmed != "" {
				m = m.appendHistory("$ "+trimmed, m.runLine(trimmed))
			}
			m.input.SetValue("")
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m replModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("gym repl")
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	body := strings.Join(m.history, "\n")
	return title + "\n" + muted.Render("press Enter to run, Ctrl+C to quit") + "\n\n" + body + "\n\n" + m.input.View() + "\n"
}

func (a *App) cmdREPL() error {
	m := newREPLModel(a)
	_, err := tea.NewProgram(m).Run()
	return err
}
