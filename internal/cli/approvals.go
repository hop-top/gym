package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/install"
)

// approvalTarget resolves the <skill|dir> argument of approve/revoke.
// A directory yields its base name (the name `gym learn <dir>`
// installs under) and its computed digest; a bare name needs --digest
// for approve.
func approvalTarget(arg, digestFlag string) (skill, source, digest string, err error) {
	if digestFlag != "" && !install.ValidDigest(digestFlag) {
		return "", "", "", fmt.Errorf("invalid --digest %q (want sha256:<64 lowercase hex>)", digestFlag)
	}
	if st, statErr := os.Stat(arg); statErr == nil && st.IsDir() {
		abs, err := filepath.Abs(arg)
		if err != nil {
			return "", "", "", err
		}
		computed, err := install.Digest(abs)
		if err != nil {
			return "", "", "", err
		}
		if digestFlag != "" && digestFlag != computed {
			return "", "", "", fmt.Errorf("--digest %s does not match the directory digest %s", digestFlag, computed)
		}
		return filepath.Base(abs), abs, computed, nil
	}
	return arg, "", digestFlag, nil
}

func operatorName() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "operator"
}

func (a *App) approveCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "approve <skill|dir>",
		Short: "Record approval of a skill at an exact content digest",
		Long: "Record operator approval of a skill. The approval is bound to the content\n" +
			"digest: any change to the skill returns it to unreviewed.\n" +
			"A directory argument computes its digest; a skill name needs --digest.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, _ := cmd.Flags().GetString("digest")
			skill, source, digest, err := approvalTarget(args[0], d)
			if err != nil {
				return err
			}
			if digest == "" {
				return fmt.Errorf("approve %q: pass a skill directory or --digest sha256:<hex>", skill)
			}
			st, err := approvals.Open("")
			if err != nil {
				return err
			}
			if err := st.Approve(skill, source, digest, operatorName(), time.Now()); err != nil {
				return err
			}
			a.printlnf("approved %s %s", skill, digest)
			return nil
		},
	}
	c.Flags().String("digest", "", "content digest to approve (sha256:<hex>)")
	return c
}

func (a *App) revokeCobra() *cobra.Command {
	c := &cobra.Command{
		Use:   "revoke <skill|dir>",
		Short: "Revoke a skill (every digest) or one digest",
		Long: "Record operator revocation. A skill name revokes it at every digest;\n" +
			"with --digest only that digest is revoked. A directory argument revokes\n" +
			"its name unless --digest is given.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, _ := cmd.Flags().GetString("digest")
			skill, _, _, err := approvalTarget(args[0], d)
			if err != nil {
				return err
			}
			st, err := approvals.Open("")
			if err != nil {
				return err
			}
			if err := st.Revoke(skill, d, operatorName(), time.Now()); err != nil {
				return err
			}
			if d == "" {
				a.printlnf("revoked %s (all digests)", skill)
			} else {
				a.printlnf("revoked %s %s", skill, d)
			}
			return nil
		},
	}
	c.Flags().String("digest", "", "revoke only this digest (sha256:<hex>)")
	return c
}
