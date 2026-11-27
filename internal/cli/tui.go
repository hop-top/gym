// Package cli — tui.go: gym's interactive Terminal UI built on top of
// kit/console/tui (bubbletea/v2 wrapper) so look-and-feel matches tlc and
// other hop.top tools.
package cli

import (
	"bytes"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	kitcli "hop.top/kit/go/console/cli"
	kittui "hop.top/kit/go/console/tui"
	tuistyles "hop.top/kit/go/console/tui/styles"
)

// menuEntry is a selectable action in the gym tui main list. It implements
// kittui.Renderer so it can live inside a kit/tui.List.
type menuEntry struct {
	title    string
	desc     string
	action   string
	selected bool
	styles   tuistyles.Styles
}

// Render satisfies kittui.Renderer.
func (m menuEntry) Render(width int) string {
	prefix := "  "
	titleStyle := m.styles.Bold
	if m.selected {
		prefix = m.styles.Accent.Render("> ")
		titleStyle = m.styles.Accent.Bold(true)
	}
	line := prefix + titleStyle.Render(m.title)
	if m.desc != "" {
		line += "  " + m.styles.Muted.Render(m.desc)
	}
	if width > 0 && lipgloss.Width(line) > width {
		// Soft-truncate to width to avoid wrapping in narrow terminals.
		return line[:width]
	}
	return line
}

// tuiModel is the top-level bubbletea/v2 model for `gym tui`.
type tuiModel struct {
	app      *App
	common   tuistyles.Common
	entries  []menuEntry
	cursor   int
	list     kittui.List
	status   string
	statusOK bool

	learnIn bool
	input   textinput.Model
}

// newTUIModel constructs the model with the given theme + dimensions.
func newTUIModel(app *App, theme kitcli.Theme, width, height int) tuiModel {
	common := tuistyles.NewCommon(theme, width, height)

	entries := []menuEntry{
		{title: "Update registry", desc: "Refresh local registry references", action: "update"},
		{title: "Train skills", desc: "Upgrade installed skills (policy aware)", action: "train"},
		{title: "Learn skill", desc: "Install by name or URL", action: "learn"},
		{title: "Show paths", desc: "List configured and detected paths", action: "paths"},
	}

	in := textinput.New()
	in.Placeholder = "skill name or URL"
	in.Prompt = "learn> "
	in.SetStyles(kittui.TextInputStyles(theme))

	m := tuiModel{
		app:     app,
		common:  common,
		entries: entries,
		cursor:  0,
		list:    kittui.NewList(common.ContentHeight()),
		status:  "ready",
		input:   in,
	}
	return m.rebuildList()
}

// rebuildList rebuilds the kit/tui.List from current entries + cursor.
func (m tuiModel) rebuildList() tuiModel {
	items := make([]kittui.Item, len(m.entries))
	for i, e := range m.entries {
		e.selected = i == m.cursor
		e.styles = m.common.Styles
		items[i] = e
	}
	m.list = m.list.SetItems(items)
	return m
}

// Init satisfies tea.Model. Requests an initial window-size message so
// the list can size itself.
func (m tuiModel) Init() tea.Cmd { return tea.RequestWindowSize }

// runManaged runs a gym subcommand against a buffered output and
// returns the captured text. App.Stdout/Stderr are swapped temporarily
// so output is captured in-band rather than spraying over the TUI.
func (m tuiModel) runManaged(args []string) (string, bool) {
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
			return "error: " + err.Error(), false
		}
		return out + "\nerror: " + err.Error(), false
	}
	if out == "" {
		return "ok", true
	}
	return out, true
}

// Update satisfies tea.Model.
func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.common.Width = msg.Width
		m.common.Height = msg.Height
		m.list = m.list.SetHeight(m.common.ContentHeight())
		return m.rebuildList(), nil
	}

	if m.learnIn {
		return m.updateLearnInput(msg)
	}
	return m.updateMenu(msg)
}

func (m tuiModel) updateLearnInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			m.learnIn = false
			m.input.SetValue("")
			return m, nil
		case "enter":
			v := strings.TrimSpace(m.input.Value())
			if v != "" {
				m.status, m.statusOK = m.runManaged([]string{"learn", v})
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

func (m tuiModel) updateMenu(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
			return m.rebuildList(), nil
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
			return m.rebuildList(), nil
		case "enter":
			if m.cursor < 0 || m.cursor >= len(m.entries) {
				return m, nil
			}
			entry := m.entries[m.cursor]
			if entry.action == "learn" {
				m.learnIn = true
				m.input.Focus()
				return m, nil
			}
			m.status, m.statusOK = m.runManaged([]string{entry.action})
			return m, nil
		}
	}
	return m, nil
}

// View satisfies tea.Model.
func (m tuiModel) View() tea.View {
	s := m.common.Styles
	w := m.common.Width
	if w <= 0 {
		w = 80
	}

	header := s.Header.Width(w).Render("gym tui  " + s.Muted.Render("(up/down select, enter run, q quit)"))
	footer := s.Footer.Width(w).Render(m.renderStatus())

	var body string
	if m.learnIn {
		body = s.Main.Render(
			s.Bold.Render("Install a skill") + "\n" +
				m.input.View() + "\n\n" +
				s.Muted.Render("enter: run, esc: cancel"),
		)
	} else {
		body = s.Main.Render(m.list.View(w - 4))
	}

	contentH := m.common.ContentHeight()
	if contentH > 0 {
		body = lipgloss.NewStyle().Height(contentH).Render(body)
	}

	out := strings.Join([]string{header, body, footer}, "\n")
	height := m.common.Height
	if height <= 0 {
		height = lipgloss.Height(out)
	}
	return tea.NewView(lipgloss.Place(w, height, lipgloss.Left, lipgloss.Top, out))
}

// renderStatus formats the most-recent operation outcome with theme styling.
func (m tuiModel) renderStatus() string {
	s := m.common.Styles
	label := s.Muted.Render("status:")
	if m.status == "" {
		return label + " " + s.Muted.Render("ready")
	}
	first := strings.SplitN(m.status, "\n", 2)[0]
	if m.statusOK {
		return fmt.Sprintf("%s %s", label, s.Success.Render(first))
	}
	return fmt.Sprintf("%s %s", label, s.Error.Render(first))
}

// cmdTUI launches the gym TUI. Theme and dimensions come from the kit
// root; the bubbletea program runs against the actual terminal.
func (a *App) cmdTUI() error {
	theme := a.theme
	m := newTUIModel(a, theme, 0, 0)
	_, err := tea.NewProgram(m).Run()
	return err
}
