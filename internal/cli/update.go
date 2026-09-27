package cli

import (
	"fmt"

	"github.com/harshalranjhani/preview-cli/internal/ui"
	"github.com/harshalranjhani/preview-cli/internal/update"
	"github.com/spf13/cobra"
)

func newUpdate() *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Short:   "Replace this binary with the latest GitHub release",
		Args:    cobra.NoArgs,
		Example: `  sudo preview update`,
		RunE: func(cmd *cobra.Command, args []string) error {
			result, err := update.Run(update.Options{Current: version})
			if err != nil {
				return err
			}
			if flagJSON {
				return ui.WriteJSON(cmd.OutOrStdout(), map[string]any{
					"updated": result.Updated,
					"from":    result.From,
					"to":      result.To,
					"path":    result.Path,
				})
			}
			if !result.Updated {
				fmt.Fprintf(cmd.OutOrStdout(), "preview %s is already the latest release\n", result.To)
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ Updated %s from %s to %s\n", result.Path, result.From, result.To)
			return nil
		},
	}
}
