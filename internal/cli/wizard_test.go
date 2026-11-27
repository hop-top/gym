package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestRunCoachHireWizard_Headless feeds canned answers to the line
// frontend and checks all four prompts are captured + returned in
// coachHirePrompts. Mirrors kit's wizard line-frontend tests.
func TestRunCoachHireWizard_Headless(t *testing.T) {
	app := &App{
		Stdin:  strings.NewReader("hop-top/skills\nhopbot\nyes\nno\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		CWD:    t.TempDir(),
	}
	got, err := runCoachHireWizard(context.Background(), app)
	if err != nil {
		t.Fatalf("wizard error: %v", err)
	}
	if got.Ref != "hop-top/skills" {
		t.Errorf("ref: got %q want %q", got.Ref, "hop-top/skills")
	}
	if got.Name != "hopbot" {
		t.Errorf("name: got %q want %q", got.Name, "hopbot")
	}
	if !got.Curated {
		t.Errorf("curated: got false, want true")
	}
	if got.Global {
		t.Errorf("global: got true, want false")
	}
}

// TestRunCoachHireWizard_RequiredRef checks that an empty required
// field re-prompts (kit/wizard line frontend's contract) rather than
// silently returning an empty struct.
func TestRunCoachHireWizard_RequiredRef(t *testing.T) {
	// First answer empty (rejected), second answer valid.
	app := &App{
		Stdin:  strings.NewReader("\nhop-top/skills\nhopbot\nyes\nno\n"),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		CWD:    t.TempDir(),
	}
	got, err := runCoachHireWizard(context.Background(), app)
	if err != nil {
		t.Fatalf("wizard error: %v", err)
	}
	if got.Ref != "hop-top/skills" {
		t.Errorf("ref: got %q want %q", got.Ref, "hop-top/skills")
	}
}
