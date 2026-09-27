package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/doctor"
	"github.com/harshalranjhani/preview-cli/internal/ui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	version    = "dev"
	flagConfig string
	flagJSON   bool
	flagDebug  bool
)

// Execute runs the preview CLI and returns a process status code.
func Execute(v string) int {
	if v != "" {
		version = v
	}
	root := newRoot()
	err := root.Execute()
	if err == nil || clierr.CodeOf(err) == "DOCTOR_FAILED" {
		if err == nil {
			return 0
		}
		return clierr.ExitOf(err)
	}
	uiPrintErr(err)
	return clierr.ExitOf(err)
}

func uiPrintErr(err error) {
	msg := err.Error()
	if flagJSON {
		_ = ui.WriteJSON(os.Stdout, map[string]any{
			"error": map[string]string{
				"code":    clierr.CodeOf(err),
				"message": msg,
			},
		})
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
}

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "preview",
		Short:         "Publish a local HTTP port on a temporary HTTPS hostname",
		SilenceUsage:  true,
		SilenceErrors: true,
		Example: `  preview http 3000 --name auth-fix
  preview list
  preview stop pv_k7p2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	root.PersistentFlags().StringVar(&flagConfig, "config", "", "server config file (default /etc/preview/config.yaml or $PREVIEW_CONFIG)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "print machine-readable JSON")
	root.PersistentFlags().BoolVar(&flagDebug, "debug", false, "print troubleshooting details to stderr")

	root.AddCommand(
		newHTTP(),
		newStop(),
		newList(),
		newStatus(),
		newInit(),
		newDoctor(),
		newGC(),
		newServer(),
		newVersion(),
		newCompletion(root),
	)
	return root
}

func newVersion() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the preview version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			if flagJSON {
				_ = ui.WriteJSON(cmd.OutOrStdout(), map[string]string{"version": version})
				return
			}
			fmt.Fprintln(cmd.OutOrStdout(), "preview", version)
		},
	}
}

func newCompletion(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:       "completion [bash|zsh|fish]",
		Short:     "Print a shell completion script",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(out)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			default:
				return clierr.Usage("unsupported shell %q (use bash, zsh, or fish)", args[0])
			}
		},
	}
}

func printReport(w io.Writer, report doctor.Report) {
	if flagJSON {
		_ = ui.WriteJSON(w, report)
		return
	}
	for _, check := range report.Checks {
		mark := "✓"
		switch check.Status {
		case "warning":
			mark = "!"
		case "error":
			mark = "✗"
		}
		if strings.Contains(check.Detail, "\n") {
			fmt.Fprintf(w, "%s %s\n%s\n", mark, check.Name, check.Detail)
			continue
		}
		fmt.Fprintf(w, "%s %-18s %s\n", mark, check.Name, check.Detail)
	}
	errors, warnings := report.Counts()
	fmt.Fprintf(w, "\n%d error(s), %d warning(s)\n", errors, warnings)
}

func doctorFailed(report doctor.Report) error {
	if report.OK {
		return nil
	}
	return clierr.New(1, "DOCTOR_FAILED", "preview server is not ready")
}

func isTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}
