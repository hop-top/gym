// Package cli — cheat.go: `gym cheat` subcommand. Renders cheat-sheet
// entries for gym itself and installed skills via kit/console/markdown,
// or exports a tip-compatible adapter file.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/ai/toolspec"
	"hop.top/kit/go/console/markdown"

	"hop.top/gym/internal/cheat"
	"hop.top/gym/internal/state"
)

// cheatCobra wires `gym cheat` and its --tip-export / --reindex flags.
// Behaviours:
//   - `gym cheat <topic>`         render the matching toolspec entry as
//     markdown (kit/console/markdown).
//   - `gym cheat --tip-export`    write tip adapter JSON file. With
//     --path PATH the destination is overridden.
//   - `gym cheat --reindex`       rebuild the merged registry from
//     currently-installed skills (and emit a one-line summary).
func (a *App) cheatCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "cheat [topic]",
		Short: "Render gym/skill cheat-sheets and export tip adapter",
		Long: "gym cheat surfaces the toolspec registry built at boot. " +
			"Without arguments and without flags it lists known topics. " +
			"With a topic argument it renders the matching command/skill " +
			"as markdown. With --tip-export it writes a tip-compatible " +
			"adapter file at <tip-config>/adapters/gym.json.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tipExport, _ := cmd.Flags().GetBool("tip-export")
			reindex, _ := cmd.Flags().GetBool("reindex")
			pathOverride, _ := cmd.Flags().GetString("path")

			reg, err := a.cheatRegistry()
			if err != nil {
				return err
			}

			switch {
			case reindex:
				return a.cmdCheatReindex(reg)
			case tipExport:
				return a.cmdCheatTipExport(reg, pathOverride)
			case len(args) == 1:
				return a.cmdCheatTopic(reg, args[0])
			default:
				return a.cmdCheatList(reg)
			}
		},
	}
	c.Flags().Bool("tip-export", false, "write the tip adapter JSON file")
	c.Flags().Bool("reindex", false, "rebuild the merged registry from installed skills")
	c.Flags().String("path", "", "override --tip-export destination path")
	return c
}

// cheatRegistry rebuilds the cheat registry on demand. Self-spec walks
// the live cobra tree; skill specs come from installed.json.
func (a *App) cheatRegistry() (*cheat.Registry, error) {
	root := a.newKitRoot().Cmd
	self := cheat.BuildSelfSpec(root, resolveVersion())

	rt, err := a.loadRuntime()
	if err != nil {
		// Even when state is unavailable, return a registry containing
		// just the self-spec so `gym cheat <topic>` still works for the
		// gym command tree.
		reg := cheat.NewRegistry()
		reg.Update(self, nil)
		return reg, nil
	}
	var installed []state.InstalledSkill
	if g, err := state.LoadGlobal(rt.Dirs.StateHome); err == nil {
		installed = append(installed, g...)
	}
	if rt.InProject {
		if p, err := state.LoadProject(rt.ProjectRoot); err == nil {
			installed = append(installed, p...)
		}
	}
	skillSpecs := cheat.BuildSkillSpecs(installed)

	reg := cheat.NewRegistry()
	reg.Update(self, skillSpecs)
	return reg, nil
}

func (a *App) cmdCheatList(reg *cheat.Registry) error {
	self := reg.Self()
	if self == nil {
		return errors.New("no self-spec available")
	}
	a.printlnf("gym cheat — known topics:")
	for _, c := range self.Commands {
		safety := "safe"
		if c.Safety != nil {
			safety = string(c.Safety.Level)
		}
		a.printlnf("  gym %-12s [%s]", c.Name, safety)
	}
	skills := reg.Skills()
	if len(skills) > 0 {
		a.printlnf("")
		a.printlnf("installed skills:")
		for _, s := range skills {
			a.printlnf("  %s", s.Name)
		}
	}
	a.printlnf("")
	a.printlnf("usage:")
	a.printlnf("  gym cheat <topic>          render markdown for a topic")
	a.printlnf("  gym cheat --tip-export     write tip adapter file")
	a.printlnf("  gym cheat --reindex        rebuild from installed skills")
	return nil
}

func (a *App) cmdCheatTopic(reg *cheat.Registry, topic string) error {
	res, ok := reg.Lookup(topic)
	if !ok {
		return fmt.Errorf("cheat: no entry for topic %q (try `gym cheat` to list topics)", topic)
	}
	md := renderTopicMarkdown(topic, res)
	rendered, err := markdown.Render(md, a.noColor)
	if err != nil || strings.TrimSpace(rendered) == "" {
		// Fall back to raw markdown when the renderer is unhappy
		// (e.g. nontty environments without a profile).
		rendered = md
	}
	fmt.Fprint(a.Stdout, rendered)
	if !strings.HasSuffix(rendered, "\n") {
		fmt.Fprintln(a.Stdout)
	}
	return nil
}

func (a *App) cmdCheatTipExport(reg *cheat.Registry, pathOverride string) error {
	path, err := cheat.WriteTipAdapter(pathOverride, reg, resolveVersion())
	if err != nil {
		return err
	}
	a.printlnf("wrote tip adapter to %s", path)
	return nil
}

func (a *App) cmdCheatReindex(reg *cheat.Registry) error {
	// cheatRegistry already rebuilt the merged registry; this command
	// is a thin wrapper that surfaces the count for human eyes and
	// prints the JSON encoding of the self-spec so callers can pipe it.
	skills := reg.Skills()
	a.printlnf("reindexed cheat registry: %d skills tracked", len(skills))
	if self := reg.Self(); self != nil {
		blob, _ := json.MarshalIndent(self, "", "  ")
		fmt.Fprintln(a.Stdout, string(blob))
	}
	return nil
}

// renderTopicMarkdown builds a markdown blob for one Lookup result.
func renderTopicMarkdown(topic string, res cheat.LookupResult) string {
	var b strings.Builder
	switch res.Kind {
	case cheat.KindSelf:
		fmt.Fprintf(&b, "# gym\n\nSchema version: `%s`\n\n## Commands\n\n", res.Spec.SchemaVersion)
		for _, c := range res.Spec.Commands {
			writeCommandLine(&b, "gym", c)
		}
	case cheat.KindGymCommand:
		c := res.Command
		fmt.Fprintf(&b, "# gym %s\n\n", c.Name)
		if len(c.Aliases) > 0 {
			fmt.Fprintf(&b, "Aliases: `%s`\n\n", strings.Join(c.Aliases, "`, `"))
		}
		if c.Safety != nil {
			fmt.Fprintf(&b, "**Safety**: %s\n\n", c.Safety.Level)
		}
		if c.Contract != nil {
			fmt.Fprintf(&b, "**Idempotent**: %v · **Retryable**: %v\n\n", c.Contract.Idempotent, c.Contract.Retryable)
			if len(c.Contract.SideEffects) > 0 {
				fmt.Fprintf(&b, "Side effects: %s\n\n", strings.Join(c.Contract.SideEffects, ", "))
			}
		}
		if c.OutputSchema != nil && c.OutputSchema.Format != "" {
			fmt.Fprintf(&b, "Output: `%s` — %s\n\n", c.OutputSchema.Format, strings.Join(c.OutputSchema.Fields, ", "))
		}
		if len(c.Flags) > 0 {
			b.WriteString("## Flags\n\n")
			for _, f := range c.Flags {
				fmt.Fprintf(&b, "- `--%s` — %s\n", f.Name, f.Description)
			}
			b.WriteString("\n")
		}
		if len(c.Children) > 0 {
			b.WriteString("## Subcommands\n\n")
			for _, child := range c.Children {
				writeCommandLine(&b, "gym "+c.Name, child)
			}
		}
	case cheat.KindSkill:
		fmt.Fprintf(&b, "# skill: %s\n\n", res.Spec.Name)
		if len(res.Spec.Commands) > 0 {
			c := res.Spec.Commands[0]
			if c.Intent != nil && len(c.Intent.Tags) > 0 {
				fmt.Fprintf(&b, "%s\n\n", c.Intent.Tags[0])
			}
		}
		if len(res.Spec.Workflows) > 0 {
			b.WriteString("## Examples\n\n")
			for _, ex := range res.Spec.Workflows[0].Steps {
				fmt.Fprintf(&b, "- %s\n", ex)
			}
		}
	}
	if b.Len() == 0 {
		fmt.Fprintf(&b, "_no detail available for %s_\n", topic)
	}
	return b.String()
}

func writeCommandLine(b *strings.Builder, prefix string, c toolspec.Command) {
	safety := "safe"
	if c.Safety != nil {
		safety = string(c.Safety.Level)
	}
	fmt.Fprintf(b, "- `%s %s` _(%s)_\n", prefix, c.Name, safety)
}
