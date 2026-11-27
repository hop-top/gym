package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/state"
)

// skillLayer is the installed skills of one scope in a resolution chain.
type skillLayer struct {
	Scope  string // project|global
	Folder string // scope root; "" for global
	Depth  int    // 0 = innermost; the global layer follows the last folder
	Skills []state.InstalledSkill
}

// listEntry is one installed skill as `gym list` shows it.
type listEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Scope       string `json:"scope"`
	Folder      string `json:"folder,omitempty"`
	Depth       int    `json:"depth"`
	InstallPath string `json:"install_path"`
	SourceURL   string `json:"source_url,omitempty"`
	// ShadowedBy is where the same name wins instead: a scope root, or
	// "global". Set only in effective resolution.
	ShadowedBy string `json:"shadowed_by,omitempty"`
}

// resolveEffective flattens layers, given innermost first then global,
// into the effective set: the first layer to hold a name wins, and each
// later holder of that name is kept but marked ShadowedBy the winner.
func resolveEffective(layers []skillLayer) []listEntry {
	winner := map[string]string{}
	var out []listEntry
	for _, l := range layers {
		where := l.Folder
		if l.Scope == "global" {
			where = "global"
		}
		for _, s := range l.Skills {
			e := listEntry{
				Name: s.Name, Version: s.Version, Scope: l.Scope, Folder: l.Folder,
				Depth: l.Depth, InstallPath: s.InstallPath, SourceURL: s.SourceURL,
			}
			if w, ok := winner[s.Name]; ok {
				e.ShadowedBy = w
			} else {
				winner[s.Name] = where
			}
			out = append(out, e)
		}
	}
	return out
}

// skillLayers reads the installed skills of the scopes a command looks at,
// innermost folder first, global last. Records whose install_path is not
// trusted are skipped with a warning (see trustedRecords). inherit takes
// the whole folder chain; otherwise only the command's own scope root.
func (a *App) skillLayers(rt runtime, global, project, inherit bool) ([]skillLayer, error) {
	var roots []string
	if project {
		switch {
		case inherit:
			roots = rt.Chain
		case rt.InProject:
			roots = []string{rt.ProjectRoot}
		}
	}
	var layers []skillLayer
	for i, root := range roots {
		recs, err := state.LoadProject(root)
		if err != nil {
			return nil, fmt.Errorf("load project skills (%s): %w", root, err)
		}
		layers = append(layers, skillLayer{
			Scope: "project", Folder: root, Depth: i,
			Skills: a.trustedRecordsAt(rt, recs, "project", root),
		})
	}
	if global {
		recs, err := state.LoadGlobal(rt.Dirs.StateHome)
		if err != nil {
			return nil, fmt.Errorf("load global skills: %w", err)
		}
		layers = append(layers, skillLayer{
			Scope: "global", Depth: len(roots),
			Skills: a.trustedRecordsAt(rt, recs, "global", ""),
		})
	}
	return layers, nil
}

func (a *App) listCobra() *cobra.Command {
	c := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List installed skills",
		Long: "List the skills installed in one scope: the folder scope at --dir, the nearest\n" +
			"folder scope at or above the current directory, or the global scope with -g.\n" +
			"With --effective, list what an agent working in the folder sees: its scope,\n" +
			"every ancestor scope, and the global scope, the nearest holder of a name\n" +
			"winning and the others marked as shadowed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			global, _ := cmd.Flags().GetBool("global")
			effective, _ := cmd.Flags().GetBool("effective")
			format, _ := cmd.Flags().GetString("format")
			if check, _ := cmd.Flags().GetBool("check"); check {
				if effective {
					return errors.New("--check and --effective are mutually exclusive")
				}
				return a.runListCheck(global, format)
			}
			return a.runList(global, effective, format)
		},
	}
	addDirFlag(c)
	c.Flags().BoolP("global", "g", false, "list the global scope")
	c.Flags().Bool("effective", false, "include ancestor folder scopes and the global scope; nearest wins")
	c.Flags().Bool("check", false, "compare the scope with its lock; exit 1 on missing, digest-drift or modified")
	return c
}

func (a *App) runList(global, effective bool, format string) error {
	if global && a.scopeDir != "" {
		return errDirGlobal
	}
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}
	var layers []skillLayer
	switch {
	case global:
		layers, err = a.skillLayers(rt, true, false, false)
	case effective:
		layers, err = a.skillLayers(rt, true, true, true)
	default:
		layers, err = a.skillLayers(rt, false, true, false)
	}
	if err != nil {
		return err
	}
	var entries []listEntry
	if effective {
		entries = resolveEffective(layers)
	} else {
		for _, l := range layers {
			for _, e := range resolveEffective([]skillLayer{l}) {
				e.ShadowedBy = ""
				entries = append(entries, e)
			}
		}
	}
	if entries == nil {
		entries = []listEntry{}
	}

	if strings.EqualFold(format, "json") {
		enc := json.NewEncoder(a.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if !global && !effective && !rt.InProject {
		fmt.Fprintf(a.Stderr, "note: no folder scope at or above %s; -g lists the global scope\n", a.startDir())
	}
	tw := tabwriter.NewWriter(a.Stdout, 0, 4, 2, ' ', 0)
	head := "NAME\tVERSION\tSCOPE\tFOLDER\tPATH"
	if effective {
		head += "\tSHADOWED BY"
	}
	fmt.Fprintln(tw, head)
	for _, e := range entries {
		folder := e.Folder
		if folder == "" {
			folder = "-"
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%s\t%s", e.Name, e.Version, e.Scope, folder, e.InstallPath)
		if effective {
			row += "\t" + e.ShadowedBy
		}
		fmt.Fprintln(tw, row)
	}
	return tw.Flush()
}
