package cli

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	kitcli "hop.top/kit/go/console/cli"
)

// minimalTheme is a tiny kitcli.Theme that's enough for renderer tests.
// We avoid pulling in the full kit defaults so this file can build with
// the test cache cold.
func minimalTheme() kitcli.Theme {
	return kitcli.Theme{
		Accent:    lipgloss.Color("39"),
		Secondary: lipgloss.Color("240"),
		Muted:     lipgloss.Color("241"),
		Error:     lipgloss.Color("196"),
		Success:   lipgloss.Color("42"),
		Title:     lipgloss.NewStyle().Bold(true),
		Subtle:    lipgloss.NewStyle(),
		Bold:      lipgloss.NewStyle().Bold(true),
	}
}

// TestNewTUIModel checks that the model builds without panicking and
// pre-populates the four built-in entries. Bubbletea programs can't
// actually run inside `go test` without a TTY, so we exercise the
// renderer + Update logic directly.
func TestNewTUIModel(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	m := newTUIModel(app, minimalTheme(), 80, 24)

	if got := len(m.entries); got != 4 {
		t.Fatalf("expected 4 menu entries, got %d", got)
	}
	if m.entries[0].action != "update" {
		t.Errorf("first entry should be update, got %q", m.entries[0].action)
	}
	if m.cursor != 0 {
		t.Errorf("initial cursor should be 0, got %d", m.cursor)
	}
}

// TestTUIModel_KeyDownMoves verifies the cursor advances on a down keypress.
func TestTUIModel_KeyDownMoves(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	m := newTUIModel(app, minimalTheme(), 80, 24)

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	tm := updated.(tuiModel)
	if tm.cursor != 1 {
		t.Errorf("expected cursor=1 after 'j', got %d", tm.cursor)
	}
}

// TestTUIModel_View ensures the View renders without panicking and
// includes the title.
func TestTUIModel_View(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	m := newTUIModel(app, minimalTheme(), 80, 24)
	v := m.View()
	if !strings.Contains(v.Content, "gym tui") {
		t.Errorf("expected 'gym tui' in view, got %q", v.Content)
	}
}

// TestNewREPLModel verifies the model builds with the markdown banner
// in history.
func TestNewREPLModel(t *testing.T) {
	app := &App{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}
	m := newREPLModel(app, minimalTheme(), true /* noColor for stable output */)
	if len(m.history) == 0 {
		t.Fatal("expected at least one history entry (the markdown hint banner)")
	}
	if !strings.Contains(m.history[0], "gym repl") {
		t.Errorf("expected hint banner to mention gym repl, got %q", m.history[0])
	}
}
