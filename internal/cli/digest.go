package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/install"
)

// digestCobra builds `gym digest <dir>`: print the content digest that
// registry maintainers and authors pin.
func (a *App) digestCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "digest <dir|archive>",
		Short: "Print the content digest of a skill directory or archive",
		Long: "Print the sha256 content digest of a skill directory: the value a\n" +
			"registry entry's digest field pins. An archive (.tar.gz, .tgz, .zip)\n" +
			"is digested over its extracted tree, which equals the digest of the\n" +
			"same tree as a directory. --files also prints the sorted per-file\n" +
			"listing the digest is computed over.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := os.Stat(args[0])
			if err != nil {
				return err
			}
			root := args[0]
			if !st.IsDir() {
				if !install.IsArchiveSource(args[0]) {
					return fmt.Errorf("%s is not a directory or an archive", args[0])
				}
				staged, err := install.StageArchive(cmd.Context(), args[0], nil)
				if err != nil {
					return err
				}
				defer staged.Cleanup()
				root = staged.Root
			}
			files, err := install.DigestListing(root)
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
