package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/harshalranjhani/preview-cli/internal/caddy"
	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/doctor"
	"github.com/harshalranjhani/preview-cli/internal/preview"
	"github.com/harshalranjhani/preview-cli/internal/server"
	"github.com/harshalranjhani/preview-cli/internal/state"
	"github.com/harshalranjhani/preview-cli/internal/ui"
	"github.com/spf13/cobra"
)

func newServer() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Configure the Caddy site that serves preview hostnames",
	}
	cmd.AddCommand(newServerInit(), newServerApply(), newServerRender(), newServerSync())
	return cmd
}

func newServerInit() *cobra.Command {
	var opts serverInitOpts
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write preview config and install the managed Caddy site",
		Args:  cobra.NoArgs,
		Example: `  sudo preview server init
  sudo preview server init --non-interactive --domain preview.example.com --public-ip 203.0.113.10`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServerInit(cmd, opts)
		},
	}
	cmd.Flags().StringVar(&opts.Domain, "domain", "", "base domain, for example preview.example.com")
	cmd.Flags().StringVar(&opts.PublicIP, "public-ip", "", "public IPv4 of this server")
	cmd.Flags().StringVar(&opts.PublicIPv6, "public-ipv6", "", "public IPv6 of this server")
	cmd.Flags().StringVar(&opts.Email, "acme-email", "", "email for Let's Encrypt account notices")
	cmd.Flags().StringVar(&opts.Template, "hostname-template", "", "hostname template")
	cmd.Flags().StringVar(&opts.DNSProvider, "dns-provider", "", "DNS provider module, usually cloudflare")
	cmd.Flags().StringVar(&opts.DNSEnv, "dns-credentials-env", "", "environment variable that holds the DNS API token")
	cmd.Flags().StringVar(&opts.DNSToken, "dns-token", "", "DNS API token, written to dns.env and not to the config file")
	cmd.Flags().StringVar(&opts.TTL, "default-ttl", "", "default preview lifetime")
	cmd.Flags().StringVar(&opts.AdminURL, "admin-url", "", "Caddy admin API URL")
	cmd.Flags().BoolVar(&opts.NonInteractive, "non-interactive", false, "fail instead of prompting")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "overwrite a managed Caddy file that preview did not generate")
	return cmd
}

type serverInitOpts struct {
	Domain, PublicIP, PublicIPv6, Email, Template string
	DNSProvider, DNSEnv, DNSToken, TTL, AdminURL  string
	NonInteractive, Force                         bool
}

func runServerInit(cmd *cobra.Command, opts serverInitOpts) error {
	path := config.ResolvePath(flagConfig)
	cfg := config.Default()
	if loaded, err := config.LoadLoose(path); err == nil {
		cfg = loaded
	} else if !errors.Is(err, config.ErrNotFound) {
		return clierr.Usage("%s", err.Error())
	}
	cfg.ApplyDefaults()
	cfg.ApplyEnv()
	applyInitFlags(cmd, &cfg, opts)
	if err := fillInitPrompts(cmd, &cfg, &opts); err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return clierr.Usage("%s", err.Error())
	}

	if err := server.WriteAtomic(path, []byte(config.Format(cfg)), 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	step(true, "Configuration written to "+path)

	dnsPath := server.DNSEnvPath(path)
	if err := server.WriteDNSEnv(dnsPath, cfg.DNS.CredentialsEnv, opts.DNSToken); err != nil {
		return err
	}
	step(true, "DNS credentials file "+dnsPath)

	if err := server.CheckDNSModule(cfg.Caddy.Bin, cfg.DNS.Provider); err != nil {
		step(false, "Caddy DNS provider")
		return clierr.New(4, "CADDY_UNAVAILABLE", err.Error())
	}
	step(true, "Caddy DNS provider available")

	if err := installCaddy(cfg, path, opts.Force); err != nil {
		return err
	}
	step(true, "Managed Caddy configuration installed")

	if err := server.Validate(cfg.Caddy.Bin, cfg.Caddy.MainConfigPath, dnsPath, cfg.DNS.CredentialsEnv); err != nil {
		return clierr.New(4, "CADDY_UNAVAILABLE", err.Error())
	}
	step(true, "Caddy configuration valid")

	bin, err := server.Executable()
	if err != nil {
		return err
	}
	if warning, err := server.InstallSystemd(bin, dnsPath); err != nil {
		return err
	} else if warning != "" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", warning)
	} else {
		step(true, "Garbage collection timer enabled")
	}

	if err := os.MkdirAll(filepath.Dir(cfg.State.Path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	server.AdoptSudoUser(filepath.Dir(cfg.State.Path))
	step(true, "State directory "+filepath.Dir(cfg.State.Path))

	if err := server.Restart(cfg.Caddy.Bin, cfg.Caddy.MainConfigPath, dnsPath, cfg.DNS.CredentialsEnv); err != nil {
		return caddyUnavailable(err)
	}
	step(true, "Caddy restarted")

	rt := runtimeFrom(path, cfg)
	if err := rt.deps.Caddy.WaitUntilReady(cmd.Context()); err != nil {
		return caddyUnavailable(err)
	}
	if n, err := preview.Sync(cmd.Context(), rt.deps); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not restore existing routes: %v\n", err)
	} else if n > 0 {
		step(true, fmt.Sprintf("Restored %d preview route(s)", n))
	}

	dnsInstructions(cfg.Server.BaseDomain, cfg.Server.PublicIP, cfg.Server.PublicIPv6)
	fmt.Fprintln(cmd.OutOrStdout())
	report := runDoctor(cmd, rt)
	printReport(cmd.OutOrStdout(), report)
	return doctorFailed(report)
}

func applyInitFlags(cmd *cobra.Command, cfg *config.Config, opts serverInitOpts) {
	set := func(name, value string, dest *string) {
		if cmd.Flags().Changed(name) {
			*dest = value
		}
	}
	set("domain", opts.Domain, &cfg.Server.BaseDomain)
	set("public-ip", opts.PublicIP, &cfg.Server.PublicIP)
	set("public-ipv6", opts.PublicIPv6, &cfg.Server.PublicIPv6)
	set("acme-email", opts.Email, &cfg.Server.ACMEEmail)
	set("hostname-template", opts.Template, &cfg.Server.HostnameTemplate)
	set("dns-provider", opts.DNSProvider, &cfg.DNS.Provider)
	set("dns-credentials-env", opts.DNSEnv, &cfg.DNS.CredentialsEnv)
	set("default-ttl", opts.TTL, &cfg.Preview.DefaultTTL)
	set("admin-url", opts.AdminURL, &cfg.Caddy.AdminURL)
}

func fillInitPrompts(cmd *cobra.Command, cfg *config.Config, opts *serverInitOpts) error {
	if opts.NonInteractive || !isTerminal() {
		if cfg.Server.BaseDomain == "" {
			return clierr.Usage("base domain is required; pass --domain preview.example.com --non-interactive")
		}
		return nil
	}
	var err error
	cfg.Server.BaseDomain, err = ask("Preview base domain", cfg.Server.BaseDomain)
	if err != nil {
		return err
	}
	cfg.Server.PublicIP, err = ask("Public IPv4 of this server", cfg.Server.PublicIP)
	if err != nil {
		return err
	}
	cfg.Server.PublicIPv6, err = ask("Public IPv6 (optional)", cfg.Server.PublicIPv6)
	if err != nil {
		return err
	}
	cfg.Server.ACMEEmail, err = ask("ACME email (optional)", cfg.Server.ACMEEmail)
	if err != nil {
		return err
	}
	cfg.Server.HostnameTemplate, err = ask("Hostname template", cfg.Server.HostnameTemplate)
	if err != nil {
		return err
	}
	cfg.DNS.Provider, err = ask("DNS provider", cfg.DNS.Provider)
	if err != nil {
		return err
	}
	cfg.DNS.CredentialsEnv, err = ask("Credential environment variable", cfg.DNS.CredentialsEnv)
	if err != nil {
		return err
	}
	token, err := promptSecret("DNS API token (blank keeps the current dns.env value)")
	if err != nil {
		return err
	}
	if token != "" {
		opts.DNSToken = token
	}
	cfg.Preview.DefaultTTL, err = ask("Default preview lifetime", cfg.Preview.DefaultTTL)
	if err != nil {
		return err
	}
	cfg.Caddy.AdminURL, err = ask("Caddy admin API", cfg.Caddy.AdminURL)
	return err
}

func ask(label, current string) (string, error) {
	prompt := label
	if current != "" {
		prompt = fmt.Sprintf("%s [%s]", label, current)
	}
	value, err := promptLine(prompt)
	if err != nil {
		return "", err
	}
	if value == "" {
		return current, nil
	}
	return value, nil
}

func newServerApply() *cobra.Command {
	var dryRun, force bool
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Regenerate the managed Caddy site from the config file",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			snippet, err := server.RenderSnippet(rt.cfg, rt.path)
			if err != nil {
				return clierr.Usage("%s", err.Error())
			}
			out := cmd.OutOrStdout()
			if dryRun {
				if flagJSON {
					return ui.WriteJSON(out, map[string]any{
						"managed_config_path": rt.cfg.Caddy.ManagedConfigPath,
						"main_config_path":    rt.cfg.Caddy.MainConfigPath,
						"caddyfile":           snippet,
					})
				}
				fmt.Fprintf(out, "Would write %s\n\n%s", rt.cfg.Caddy.ManagedConfigPath, snippet)
				return nil
			}
			if err := installCaddy(rt.cfg, rt.path, force); err != nil {
				return err
			}
			dnsPath := server.DNSEnvPath(rt.path)
			if err := server.Validate(rt.cfg.Caddy.Bin, rt.cfg.Caddy.MainConfigPath, dnsPath, rt.cfg.DNS.CredentialsEnv); err != nil {
				return clierr.New(4, "CADDY_UNAVAILABLE", err.Error())
			}
			if err := server.Reload(rt.cfg.Caddy.Bin, rt.cfg.Caddy.MainConfigPath, dnsPath, rt.cfg.DNS.CredentialsEnv); err != nil {
				return clierr.New(4, "CADDY_UNAVAILABLE", err.Error())
			}
			if err := rt.deps.Caddy.WaitUntilReady(cmd.Context()); err != nil {
				return caddyUnavailable(err)
			}
			n, err := preview.Sync(cmd.Context(), rt.deps)
			if err != nil {
				return err
			}
			if flagJSON {
				return ui.WriteJSON(out, map[string]any{"applied": true, "restored": n})
			}
			fmt.Fprintln(out, "✓ Caddy configuration applied")
			if n > 0 {
				fmt.Fprintf(out, "✓ Restored %d preview route(s)\n", n)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Caddy site without changing the system")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite a Caddy file preview did not generate")
	return cmd
}

func newServerRender() *cobra.Command {
	return &cobra.Command{
		Use:   "render",
		Short: "Print the Caddy site preview would install",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			snippet, err := server.RenderSnippet(rt.cfg, rt.path)
			if err != nil {
				return clierr.Usage("%s", err.Error())
			}
			fmt.Fprint(cmd.OutOrStdout(), snippet)
			return nil
		},
	}
}

func newServerSync() *cobra.Command {
	return &cobra.Command{
		Use:   "sync",
		Short: "Put saved preview routes back into Caddy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := loadRuntime(cmd)
			if err != nil {
				return err
			}
			n, err := preview.Sync(cmd.Context(), rt.deps)
			if err != nil {
				return err
			}
			if flagJSON {
				return ui.WriteJSON(cmd.OutOrStdout(), map[string]any{"restored": n})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "✓ Restored %d preview route(s)\n", n)
			return nil
		},
	}
}

func installCaddy(cfg config.Config, configPath string, force bool) error {
	snippet, err := server.RenderSnippet(cfg, configPath)
	if err != nil {
		return clierr.Usage("%s", err.Error())
	}
	previousSnippet, snippetExisted := readOptional(cfg.Caddy.ManagedConfigPath)
	previousMain, mainExisted := readOptional(cfg.Caddy.MainConfigPath)
	if err := server.InstallSnippet(cfg.Caddy.ManagedConfigPath, snippet, force); err != nil {
		return err
	}
	if _, err := server.EnsureImport(cfg.Caddy.MainConfigPath, cfg.Caddy.ManagedConfigPath, cfg.Caddy.AdminURL, cfg.Server.ACMEEmail); err != nil {
		restore(cfg.Caddy.ManagedConfigPath, previousSnippet, snippetExisted)
		return err
	}
	dnsPath := server.DNSEnvPath(configPath)
	if err := server.Validate(cfg.Caddy.Bin, cfg.Caddy.MainConfigPath, dnsPath, cfg.DNS.CredentialsEnv); err != nil {
		restore(cfg.Caddy.ManagedConfigPath, previousSnippet, snippetExisted)
		restore(cfg.Caddy.MainConfigPath, previousMain, mainExisted)
		return clierr.New(4, "CADDY_UNAVAILABLE", err.Error())
	}
	return nil
}

func readOptional(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func restore(path string, previous []byte, existed bool) {
	if !existed {
		_ = os.Remove(path)
		return
	}
	_ = server.WriteAtomic(path, previous, 0o644)
}

func runtimeFrom(path string, cfg config.Config) runtime {
	cwd, _ := os.Getwd()
	pf, _ := config.FindProject(cwd)
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
	}
}

func runDoctor(cmd *cobra.Command, rt runtime) doctor.Report {
	return doctor.Run(cmd.Context(), rt.cfg, rt.path)
}

func caddyUnavailable(err error) error {
	msg := err.Error()
	if extra := server.CaddyDiagnostics(); extra != "" {
		msg += "\n\n" + extra
	}
	return clierr.New(4, "CADDY_UNAVAILABLE", msg)
}
