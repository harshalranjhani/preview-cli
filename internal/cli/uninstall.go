package cli

import (
	"fmt"
	"os"

	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/server"
	"github.com/harshalranjhani/preview-cli/internal/ui"
	"github.com/spf13/cobra"
)

func newUninstall() *cobra.Command {
	var yes, removeCaddy bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove preview, its routes, and its config from this server",
		Args:  cobra.NoArgs,
		Example: `  sudo preview uninstall
  sudo preview uninstall --caddy`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if os.Geteuid() != 0 {
				return clierr.Usage("run as root: sudo preview uninstall")
			}
			if !yes {
				if !isTerminal() {
					return clierr.Usage("rerun with --yes to remove preview without a prompt")
				}
				answer, err := promptLine("Remove preview from this server? Type yes")
				if err != nil {
					return err
				}
				if answer != "yes" {
					fmt.Fprintln(cmd.OutOrStdout(), "Uninstall cancelled")
					return nil
				}
			}
			bin, err := os.Executable()
			if err != nil {
				return err
			}
			removed, err := server.Uninstall(server.UninstallOptions{
				ConfigPath:  config.ResolvePath(flagConfig),
				BinPath:     bin,
				RemoveCaddy: removeCaddy,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return ui.WriteJSON(cmd.OutOrStdout(), map[string]any{"removed": removed})
			}
			for _, path := range removed {
				fmt.Fprintf(cmd.OutOrStdout(), "✓ Removed %s\n", path)
			}
			if !removeCaddy {
				fmt.Fprintln(cmd.OutOrStdout(), "Caddy is still installed. Remove it too with: sudo preview uninstall --caddy")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	cmd.Flags().BoolVar(&removeCaddy, "caddy", false, "also stop Caddy and remove its binary")
	return cmd
}
