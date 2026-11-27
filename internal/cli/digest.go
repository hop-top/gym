package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/install"
)

// digestCobra builds `gym digest <dir>`: print the content digest that
// registry maintainers and authors pin (see ADR-0002).
func (a *App) digestCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "digest <dir>",
		Short: "Print the content digest of a skill directory",
		Long: "Print the sha256 content digest of a skill directory: the value a\n" +
			"registry entry's digest field pins. --files also prints the sorted\n" +
			"per-file listing the digest is computed over.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			if !st.IsDir() {
				return fmt.Errorf("%s is not a directory", args[0])
			}
			files, err := install.DigestListing(args[0])
			if err != nil {
				return err
			}
			if show, _ := cmd.Flags().GetBool("files"); show {
				for _, f := range files {
					fmt.Fprintln(a.Stdout, f.Line())
				}
			}
			fmt.Fprintln(a.Stdout, install.DigestOfListing(files))
			return nil
		},
	}
	c.Flags().Bool("files", false, "also print the sorted per-file listing")
	return c
}
