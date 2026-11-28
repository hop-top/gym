// Package cli — wizard.go: kit/console/wizard flows for gym's multi-step
// commands. Today only `coach hire` opts in; `learn` is single-shot and
// fits the wizard model poorly (deferred).
package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"hop.top/kit/go/console/wizard"
)

// coachHirePrompts are the answers collected by runCoachHireWizard.
type coachHirePrompts struct {
	Ref     string
	Name    string
	Curated bool
	Global  bool
}

// runCoachHireWizard runs an interactive wizard collecting the inputs
// for `gym coach hire`. Used when the command is invoked without a
// repo/manifest argument.
//
// The wizard auto-detects the right frontend via wizard.Run:
//   - tty + interactive: line-mode prompts
//   - --quiet / non-tty: headless (errors out when required fields are blank)
//
// Returns ErrCoachHireAborted when the user cancels with ctrl-C.
func runCoachHireWizard(ctx context.Context, app *App) (coachHirePrompts, error) {
	steps := []wizard.Step{
		wizard.TextInput("ref", "Manifest URL, path or owner/repo").
			WithRequired().
			WithDescription("Examples: hop-top/skills, https://example.com/skills.json, ./skills.json").
			WithValidateText(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("required")
				}
				return nil
			}),
		wizard.TextInput("name", "Coach name (optional, derived from manifest if empty)"),
		wizard.Confirm("curated", "Mark as curated source?").
			WithDefault(false),
		wizard.Confirm("global", "Hire globally (write to user config)?").
			WithDefault(false),
	}

	w, err := wizard.New(steps...)
	if err != nil {
		return coachHirePrompts{}, err
	}

	if err := wizard.Run(ctx, w,
		wizard.WithInput(stdin(app)),
		wizard.WithOutput(app.Stderr),
		wizard.ForceLine(),
	); err != nil {
		var abort *wizard.AbortError
		if errors.As(err, &abort) {
			return coachHirePrompts{}, ErrCoachHireAborted
		}
		return coachHirePrompts{}, err
	}

	results := w.Results()
	out := coachHirePrompts{
		Ref: stringResult(results, "ref"),
	}
	out.Name = stringResult(results, "name")
	if v, ok := results["curated"].(bool); ok {
		out.Curated = v
	}
	if v, ok := results["global"].(bool); ok {
		out.Global = v
	}
	return out, nil
}

// ErrCoachHireAborted is returned by runCoachHireWizard when the user
// cancels the prompt (ctrl-c / EOF on stdin).
var ErrCoachHireAborted = errors.New("coach hire aborted")

// stringResult extracts a string value from the wizard result map,
// returning "" when the key is missing or wrong-typed.
func stringResult(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// stdin returns app.Stdin, falling back to os.Stdin. Tests inject a
// reader via App.Stdin; the regular NewApp default is os.Stdin already.
func stdin(app *App) io.Reader {
	if app.Stdin != nil {
		return app.Stdin
	}
	return os.Stdin
}
