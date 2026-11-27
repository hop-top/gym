// Package cli — repl.go: interactive REPL for gym. Hint output flows
// through kit/console/markdown so suggestions render with the same
// styling as tlc/aps.
package cli

import (
	"bytes"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	kitcli "hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/markdown"
	tuistyles "hop.top/kit/go/console/tui/styles"
)

// replHintMarkdown is the suggestion banner rendered via kit/console/markdown
// when the REPL starts. Listed inline so the binary remains self-contained.
const replHintMarkdown = "**gym repl** — interactive prompt\n\nTry one of:\n\n" +
	"- `update` — refresh the local registry\n" +
	"- `learn <name|url>` — install a skill\n" +
	"- `train [pattern]` — upgrade installed skills\n" +
	"- `paths` — list configured install paths\n" +
	"- `coach list` — show coach sources\n" +
	"- `exit` — leave the REPL\n"

// replModel is gym's REPL bubbletea/v2 model.
type replModel struct {
	app     *App
	common  tuistyles.Common
	input   textinput.Model
	history []string
	noColor bool
}

// newREPLModel constructs the model. noColor is honored by markdown.Render
// so the hint banner matches the global --no-color flag.
func newREPLModel(app *App, theme kitcli.Theme, noColor bool) replModel {
	common := tuistyles.NewCommon(theme, 0, 0)

	in := textinput.New()
	in.Prompt = "gym> "
	in.Placeholder = "type update | learn <name/url> | train [pattern] | paths | exit"
	in.Focus()
	// Theme prompt/placeholder so they match the rest of the kit TUI surface.
	st := in.Styles()
	st.Focused.Prompt = lipgloss.NewStyle().Foreground(theme.Accent)
	st.Focused.Placeholder = lipgloss.NewStyle().Foreground(theme.Muted)
	in.SetStyles(st)

	hint, err := markdown.Render(replHintMarkdown, noColor)
	if err != nil || strings.TrimSpace(hint) == "" {
		hint = replHintMarkdown
	}

	return replModel{
		app:     app,
		common:  common,
		input:   in,
		history: []string{strings.TrimRight(hint, "\n")},
		noColor: noColor,
	}
}

// Init satisfies tea.Model.
func (m replModel) Init() tea.Cmd { return tea.RequestWindowSize }

// runLine runs a single REPL command line and returns the captured
// output. App.Stdout/Stderr are swapped to a buffer so the output stays
// inside the TUI history rather than corrupting the screen.
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

// appendHistory pushes lines onto the visible history, trimming to the
// last 20 entries.
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

// Update satisfies tea.Model.
func (m replModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.common.Width = msg.Width
		m.common.Height = msg.Height
		return m, nil
	case tea.KeyPressMsg:
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

// View satisfies tea.Model.
func (m replModel) View() tea.View {
	s := m.common.Styles
	w := m.common.Width
	if w <= 0 {
		w = 80
	}

	title := s.Header.Width(w).Render("gym repl")
	muted := s.Muted.Render("press Enter to run, Ctrl+C to quit")
	body := strings.Join(m.history, "\n")
	out := strings.Join([]string{title, muted, "", body, "", m.input.View()}, "\n")
	return tea.NewView(out)
}

// cmdREPL launches the gym REPL. Theme + no-color come from the App so
// the hint banner respects the global --no-color flag set on the kit
// root.
func (a *App) cmdREPL() error {
	m := newREPLModel(a, a.theme, a.noColor)
	_, err := tea.NewProgram(m).Run()
	return err
}
