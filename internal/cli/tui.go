package cli

import (
	"bytes"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type menuItem struct {
	title  string
	desc   string
	action string
}

func (m menuItem) Title() string       { return m.title }
func (m menuItem) Description() string { return m.desc }
func (m menuItem) FilterValue() string { return m.title }

type tuiModel struct {
	app     *App
	list    list.Model
	status  string
	learnIn bool
	input   textinput.Model
}

func newTUIModel(app *App) tuiModel {
	items := []list.Item{
		menuItem{title: "Update registry", desc: "Refresh local registry references", action: "update"},
		menuItem{title: "Train skills", desc: "Upgrade installed skills (policy aware)", action: "train"},
		menuItem{title: "Learn skill", desc: "Install by name or URL", action: "learn"},
		menuItem{title: "Show paths", desc: "List configured and detected paths", action: "paths"},
	}
	l := list.New(items, list.NewDefaultDelegate(), 80, 18)
	l.Title = "gym manager"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings()

	in := textinput.New()
	in.Placeholder = "skill name or URL"
	in.Prompt = "learn> "

	return tuiModel{app: app, list: l, status: "ready", input: in}
}

func (m tuiModel) Init() tea.Cmd { return nil }

func (m tuiModel) runManaged(args []string) string {
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
		if out == "" {
			return "error: " + err.Error()
		}
		return out + "\nerror: " + err.Error()
	}
	if out == "" {
		return "ok"
	}
	return out
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.learnIn {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "esc":
				m.learnIn = false
				m.input.SetValue("")
				return m, nil
			case "enter":
				v := strings.TrimSpace(m.input.Value())
				if v != "" {
					m.status = m.runManaged([]string{"learn", v})
				}
				m.learnIn = false
				m.input.SetValue("")
				return m, nil
			}
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "enter":
			it, ok := m.list.SelectedItem().(menuItem)
			if !ok {
				return m, nil
			}
			if it.action == "learn" {
				m.learnIn = true
				m.input.Focus()
				return m, nil
			}
			m.status = m.runManaged([]string{it.action})
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m tuiModel) View() string {
	head := lipgloss.NewStyle().Bold(true).Render("gym tui") + " (enter=run, q=quit)"
	if m.learnIn {
		return head + "\n\n" + m.input.View() + "\n\nstatus:\n" + m.status + "\n"
	}
	status := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("status")
	return head + "\n\n" + m.list.View() + "\n\n" + status + ":\n" + m.status + "\n"
}

func (a *App) cmdTUI() error {
	m := newTUIModel(a)
	_, err := tea.NewProgram(m).Run()
	return err
}
