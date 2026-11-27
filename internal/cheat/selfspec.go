package cheat

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/ai/toolspec"
)

// SchemaVersion is the explicit ToolSpec.SchemaVersion pin. Bump on a breaking change to the emitted
// shape.
const SchemaVersion = "1.0"

// ToolName is the spec name. Mirrors the binary name.
const ToolName = "gym"

// safetyByName classifies each gym command we know about. Entries
// missing here default to SafetyLevelSafe via classify(). The map keys
// are dotted command paths relative to root (e.g. "coach.hire").
var safetyByName = map[string]toolspec.Safety{
	// read-only commands
	"version":    {Level: toolspec.SafetyLevelSafe},
	"paths":      {Level: toolspec.SafetyLevelSafe},
	"warmup":     {Level: toolspec.SafetyLevelSafe},
	"tui":        {Level: toolspec.SafetyLevelSafe},
	"repl":       {Level: toolspec.SafetyLevelSafe},
	"cheat":      {Level: toolspec.SafetyLevelSafe},
	"coach":      {Level: toolspec.SafetyLevelSafe},
	"coach.list": {Level: toolspec.SafetyLevelSafe},

	// Network + index mutation; idempotent but touches state files.
	"update": {Level: toolspec.SafetyLevelCaution},

	// Filesystem mutation under user/project install paths.
	"learn":         {Level: toolspec.SafetyLevelCaution},
	"train":         {Level: toolspec.SafetyLevelCaution},
	"paths.add":     {Level: toolspec.SafetyLevelCaution},
	"coach.hire":    {Level: toolspec.SafetyLevelCaution},
	"coach.dismiss": {Level: toolspec.SafetyLevelCaution},

	// Self-replacement of the gym binary.
	"update.self": {Level: toolspec.SafetyLevelDangerous, RequiresConfirmation: true},

	// Long-lived network listener.
	"serve": {Level: toolspec.SafetyLevelCaution},
}

// contractByName attaches an idempotency / side-effect contract per
// command. Anything not listed gets a benign read-only contract.
var contractByName = map[string]toolspec.Contract{
	"update":        {Idempotent: true, Retryable: true, SideEffects: []string{"writes registry index"}},
	"learn":         {Idempotent: true, Retryable: true, SideEffects: []string{"writes installed.json", "writes skill files"}},
	"train":         {Idempotent: true, Retryable: true, SideEffects: []string{"writes installed.json", "rewrites skill files"}},
	"paths.add":     {Idempotent: true, SideEffects: []string{"writes config.yaml"}},
	"coach.hire":    {Idempotent: true, Retryable: true, SideEffects: []string{"writes config.yaml", "fetches manifest"}},
	"coach.dismiss": {Idempotent: true, SideEffects: []string{"writes config.yaml"}},
	"warmup":        {Idempotent: true, Retryable: true},
	"serve":         {Idempotent: false, SideEffects: []string{"binds port", "long-running"}},
	"cheat":         {Idempotent: true, Retryable: true},
}

// outputByName declares the JSON output schema for commands we know
// emit JSON under --format json. Empty entries get no schema.
var outputByName = map[string]toolspec.OutputSchema{
	"warmup": {
		Format: "json",
		Fields: []string{"name", "description", "version", "scope", "install_path"},
	},
}

// BuildSelfSpec walks rootCmd's subtree and synthesizes a ToolSpec
// describing gym's CLI surface. version flows into the spec for
// callers that want capability negotiation (see kit/ai/toolspec/cli).
//
// Persistent flags inherited from kit/cli (--format, --quiet, …) are
// extracted from rootCmd.PersistentFlags() and surfaced once at the
// top level.
func BuildSelfSpec(rootCmd *cobra.Command, version string) *toolspec.ToolSpec {
	if rootCmd == nil {
		return &toolspec.ToolSpec{Name: ToolName, SchemaVersion: SchemaVersion}
	}
	spec := &toolspec.ToolSpec{
		Name:          ToolName,
		SchemaVersion: SchemaVersion,
		Flags:         collectFlags(rootCmd),
	}
	for _, child := range rootCmd.Commands() {
		if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		spec.Commands = append(spec.Commands, buildCommand(child, ""))
	}
	_ = version // reserved for future capability fields; spec.Name already covers identity.
	return spec
}

// buildCommand recursively materialises one cobra.Command into a
// toolspec.Command, resolving safety/contract/output via the lookup
// tables above. parentPath is the dotted ancestor chain.
func buildCommand(cmd *cobra.Command, parentPath string) toolspec.Command {
	name := cmd.Name()
	dotted := name
	if parentPath != "" {
		dotted = parentPath + "." + name
	}
	out := toolspec.Command{
		Name:    name,
		Aliases: append([]string(nil), cmd.Aliases...),
		Flags:   collectLocalFlags(cmd),
	}
	if s, ok := safetyByName[dotted]; ok {
		sc := s
		out.Safety = &sc
	} else {
		out.Safety = &toolspec.Safety{Level: toolspec.SafetyLevelSafe}
	}
	if c, ok := contractByName[dotted]; ok {
		cc := c
		out.Contract = &cc
	}
	if o, ok := outputByName[dotted]; ok {
		oc := o
		out.OutputSchema = &oc
	}
	for _, child := range cmd.Commands() {
		if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
			continue
		}
		out.Children = append(out.Children, buildCommand(child, dotted))
	}
	return out
}

// collectFlags walks both Persistent and Local flag sets on cmd. Used
// for the root spec where we want the whole inherited surface.
func collectFlags(cmd *cobra.Command) []toolspec.Flag {
	flags := []toolspec.Flag{}
	cmd.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, toolspec.Flag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			Description: oneLine(f.Usage),
		})
	})
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, toolspec.Flag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			Description: oneLine(f.Usage),
		})
	})
	return flags
}

// collectLocalFlags only walks LocalFlags so child commands don't
// re-emit the root persistent flags on every node.
func collectLocalFlags(cmd *cobra.Command) []toolspec.Flag {
	flags := []toolspec.Flag{}
	cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
		flags = append(flags, toolspec.Flag{
			Name:        f.Name,
			Short:       f.Shorthand,
			Type:        f.Value.Type(),
			Description: oneLine(f.Usage),
		})
	})
	return flags
}

// oneLine flattens a flag's multi-line usage string into a single
// space-separated line.
func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
