package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/harshalranjhani/preview-cli/internal/caddy"
	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/exitcode"
	"github.com/harshalranjhani/preview-cli/internal/preview"
	"github.com/harshalranjhani/preview-cli/internal/state"
	"github.com/harshalranjhani/preview-cli/internal/ui"
	"github.com/spf13/cobra"
)

type runtime struct {
	path string
	cfg  config.Config
	deps preview.Deps
}

func loadRuntime(cmd *cobra.Command) (runtime, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return runtime{}, err
	}
	path := config.ResolvePath(flagConfig)
	cfg, err := config.Load(path)
	if err != nil {
		if errors.Is(err, config.ErrNotFound) {
			return runtime{}, clierr.New(exitcode.Usage, "CONFIG_NOT_FOUND", fmt.Sprintf("no config file at %s\nSet up this server with:\n  sudo preview server init", path))
		}
		return runtime{}, clierr.Usage("%s", err.Error())
	}
	pf, err := config.FindProject(cwd)
	if err != nil {
		return runtime{}, clierr.Usage("%s", err.Error())
	}
	ui.Debugf(flagDebug, "config=%s state=%s domain=%s admin=%s", path, cfg.State.Path, cfg.Server.BaseDomain, cfg.Caddy.AdminURL)
	return runtime{
		path: path,
		cfg:  cfg,
		deps: preview.Deps{
			Config:  cfg,
			Store:   state.New(cfg.State.Path),
			Caddy:   caddy.New(cfg.Caddy.AdminURL),
			Project: pf,
			CWD:     cwd,
			Debug:   func(format string, args ...any) { ui.Debugf(flagDebug, format, args...) },
		},
	}, nil
}

func newHTTP() *cobra.Command {
	var name, project, ttl string
	var skipLocal, skipPublic, keep bool
	cmd := &cobra.Command{
		Use:   "http <port>",
		Short: "Publish a local HTTP port on a temporary HTTPS hostname",
		Args:  cobra.ExactArgs(1),
		Example: `  preview http 3000
  preview http 3000 --name auth-fix --ttl 30m
  preview http 3000 --name auth-fix --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			port, err := parsePort(args[0])
			if err != nil {
				return err
			}
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			result, err := preview.Create(cmd.Context(), rt.deps, preview.CreateInput{
				Port:          port,
				Name:          name,
				Project:       project,
				TTL:           ttl,
				SkipLocal:     skipLocal,
				SkipPublic:    skipPublic,
				KeepOnFailure: keep,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(result.Warnings) > 0 {
				result.View["warnings"] = result.Warnings
			}
			if flagJSON {
				return ui.WriteJSON(out, result.View)
			}
			if skipLocal {
				fmt.Fprintf(out, "✓ Target check skipped\n")
			} else {
				fmt.Fprintf(out, "✓ Target reachable: %s\n", result.Preview.TargetURL())
			}
			fmt.Fprintf(out, "✓ Route created\n")
			if rt.cfg.VerifyPublic() && !skipPublic && len(result.Warnings) == 0 {
				fmt.Fprintf(out, "✓ HTTPS reachable\n")
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, result.Preview.PublicURL())
			fmt.Fprintln(out)
			if result.Preview.ExpiresAt == nil {
				fmt.Fprintln(out, "Expires: never")
			} else {
				fmt.Fprintf(out, "Expires in %s\n", result.View["expires_in"])
			}
			fmt.Fprintf(out, "Preview ID: %s\n", result.Preview.ID)
			for _, warning := range result.Warnings {
				fmt.Fprintf(out, "\nWarning: %s\n", warning)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "short task name included in the hostname")
	cmd.Flags().StringVar(&project, "project", "", "project name (default: .preview.yaml, git root, or directory)")
	cmd.Flags().StringVar(&ttl, "ttl", "", "lifetime, for example 30m, 2h, 1d, or 0 for no expiry")
	cmd.Flags().BoolVar(&skipLocal, "skip-local-check", false, "do not check that the port is accepting connections")
	cmd.Flags().BoolVar(&skipPublic, "no-public-check", false, "do not request the preview URL after creating it")
	cmd.Flags().BoolVar(&keep, "keep-on-failure", false, "keep the route if the HTTPS check fails")
	return cmd
}

func newStop() *cobra.Command {
	var project bool
	var all bool
	cmd := &cobra.Command{
		Use:   "stop [id|hostname]",
		Short: "Remove a preview route without stopping the application",
		Args:  cobra.MaximumNArgs(1),
		Example: `  preview stop pv_k7p2
  preview stop k7p
  preview stop --project
  preview stop --all`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if project && all {
				return clierr.Usage("use either --project or --all")
			}
			if len(args) == 0 && !project && !all {
				return clierr.Usage("provide a preview id or hostname, or pass --project or --all")
			}
			if len(args) == 1 && (project || all) {
				return clierr.Usage("do not combine an id with --project or --all")
			}
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			in := preview.StopInput{All: all, CurrentProject: project, UID: os.Getuid()}
			if len(args) == 1 {
				in.Ref = args[0]
			}
			result, err := preview.Stop(cmd.Context(), rt.deps, in)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				ids := make([]string, 0, len(result.Stopped))
				for _, p := range result.Stopped {
					ids = append(ids, p.ID)
				}
				return ui.WriteJSON(out, map[string]any{
					"stopped":  ids,
					"warnings": result.Warnings,
				})
			}
			if len(result.Stopped) == 0 {
				fmt.Fprintln(out, "No matching previews")
				return nil
			}
			if len(result.Stopped) == 1 {
				fmt.Fprintf(out, "✓ Preview %s stopped\n", result.Stopped[0].ID)
			} else {
				fmt.Fprintf(out, "✓ Stopped %d previews\n", len(result.Stopped))
			}
			for _, warning := range result.Warnings {
				fmt.Fprintf(out, "Warning: %s\n", warning)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&project, "project", false, "stop previews for the current project")
	cmd.Flags().BoolVar(&all, "all", false, "stop previews created by this user; root stops every preview")
	return cmd
}

func newList() *cobra.Command {
	var project string
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List previews on this server",
		Args:    cobra.NoArgs,
		Example: "  preview list --project dashboard",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			filter := ""
			if cmd.Flags().Changed("project") {
				filter = project
				if filter == "" || filter == "auto" {
					name, _, err := config.ResolveProjectName("", rt.deps.Project, rt.deps.CWD)
					if err != nil {
						return clierr.Usage("%s", err.Error())
					}
					filter = name
				}
			}
			items, err := preview.List(rt.deps, filter)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				if items == nil {
					items = []state.Preview{}
				}
				return ui.WriteJSON(out, map[string]any{"previews": items})
			}
			return printPreviewTable(out, items)
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "filter by project; with no value, use the current project")
	cmd.Flags().Lookup("project").NoOptDefVal = "auto"
	return cmd
}

func newStatus() *cobra.Command {
	return &cobra.Command{
		Use:   "status <id|hostname>",
		Short: "Show one preview and check that it still routes",
		Example: `  preview status pv_k7p2
  preview status k7p`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			st, err := preview.Inspect(cmd.Context(), rt.deps, args[0])
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return ui.WriteJSON(out, st)
			}
			fmt.Fprintf(out, "ID:         %s\n", st.ID)
			fmt.Fprintf(out, "Project:    %s\n", st.Project)
			if st.Name != "" {
				fmt.Fprintf(out, "Name:       %s\n", st.Name)
			}
			fmt.Fprintf(out, "Target:     %s\n", st.TargetURL())
			fmt.Fprintf(out, "URL:        %s\n", st.URL)
			fmt.Fprintf(out, "Route:      %s\n", yesNo(st.RoutePresent, "present", "missing"))
			fmt.Fprintf(out, "Local:      %s\n", yesNo(st.LocalReachable, "reachable", "unreachable"))
			if st.PublicStatus > 0 {
				fmt.Fprintf(out, "Public:     HTTP %d\n", st.PublicStatus)
			} else {
				fmt.Fprintf(out, "Public:     unreachable\n")
			}
			if st.PublicError != "" {
				fmt.Fprintf(out, "Detail:     %s\n", st.PublicError)
			}
			fmt.Fprintf(out, "Created:    %s\n", st.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
			fmt.Fprintf(out, "Expires:    %s\n", st.ExpiresIn)
			fmt.Fprintf(out, "User:       uid %d\n", st.CreatedByUID)
			if st.CWD != "" {
				fmt.Fprintf(out, "Directory:  %s\n", st.CWD)
			}
			return nil
		},
	}
}

func newInit() *cobra.Command {
	var project, prefix, ttl string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a project .preview.yaml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			if project == "" && isTerminal() && !flagJSON {
				detected, _, _ := config.ResolveProjectName("", config.ProjectFile{}, cwd)
				project, err = promptLine(fmt.Sprintf("Project name [%s]", detected))
				if err != nil {
					return err
				}
				if project == "" {
					project = detected
				}
			}
			if project == "" {
				project, _, err = config.ResolveProjectName("", config.ProjectFile{}, cwd)
				if err != nil {
					return clierr.Usage("%s", err.Error())
				}
			}
			slug, err := config.ResolveName(project, "")
			if err != nil {
				return clierr.Usage("%s", err.Error())
			}
			if slug == "" {
				return clierr.Usage("project name is required")
			}
			dir := config.ProjectDir(cwd)
			path := filepath.Join(dir, ".preview.yaml")
			if _, err := os.Stat(path); err == nil && !force {
				return clierr.Usage("%s already exists; pass --force to replace it", path)
			}
			pf := config.ProjectFile{Project: slug, DefaultNamePrefix: prefix, DefaultTTL: ttl}
			if ttl != "" {
				if _, _, err := config.ParseTTL(ttl); err != nil {
					return clierr.Usage("%s", err.Error())
				}
			}
			if prefix != "" {
				if _, err := config.ResolveName(prefix, ""); err != nil {
					return clierr.Usage("%s", err.Error())
				}
			}
			if err := config.WriteProject(path, pf); err != nil {
				return err
			}
			if flagJSON {
				return ui.WriteJSON(cmd.OutOrStdout(), map[string]string{"path": path, "project": slug})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ Wrote %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&project, "project", "", "project name")
	cmd.Flags().StringVar(&prefix, "name-prefix", "", "default preview name when --name is omitted")
	cmd.Flags().StringVar(&ttl, "ttl", "", "default preview lifetime for this project")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing .preview.yaml")
	return cmd
}

func newDoctor() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check DNS, Caddy, and local preview configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			report := runDoctor(cmd, rt)
			printReport(cmd.OutOrStdout(), report)
			return doctorFailed(report)
		},
	}
}

func newGC() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove expired previews and routes whose targets are gone",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			result, err := preview.GC(cmd.Context(), rt.deps, dryRun)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if flagJSON {
				return ui.WriteJSON(out, result)
			}
			if len(result.Removed) == 0 {
				fmt.Fprintln(out, "No stale previews")
				return nil
			}
			verb := "Removed"
			if dryRun {
				verb = "Would remove"
			}
			for _, item := range result.Removed {
				label := item.ID
				if label == "" {
					label = item.RouteID
				}
				fmt.Fprintf(out, "%s %s (%s)\n", verb, label, item.Reason)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed")
	return cmd
}

func yesNo(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
