package doctor

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/caddy"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/health"
	"github.com/harshalranjhani/preview-cli/internal/hostname"
	"github.com/harshalranjhani/preview-cli/internal/server"
)

// Check is one diagnostic result.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Report is the full doctor result.
type Report struct {
	Checks []Check `json:"checks"`
	OK     bool    `json:"ok"`
}

// Run checks the local preview installation.
func Run(ctx context.Context, cfg config.Config, configPath string) Report {
	var checks []Check
	add := func(status, name, detail string) {
		checks = append(checks, Check{Name: name, Status: status, Detail: detail})
	}

	add("ok", "config file", configPath)

	if cfg.Server.BaseDomain == "" {
		add("error", "base domain", "server.base_domain is empty")
	} else {
		add("ok", "base domain", cfg.Server.BaseDomain)
	}

	host, err := hostname.Build(cfg.Server.HostnameTemplate, "demo", "task", "ab12", cfg.Server.BaseDomain)
	if err != nil {
		add("error", "hostname template", err.Error())
	} else if !hostname.CoversDomain(cfg.Server.HostnameTemplate, cfg.Server.BaseDomain) {
		add("warning", "hostname template", fmt.Sprintf("example %s may not be covered by *.%s", host, cfg.Server.BaseDomain))
	} else {
		add("ok", "hostname template", host)
	}

	if err := config.ValidateTarget(cfg.Routing.TargetHost, cfg.AllowNonLoopback()); err != nil {
		add("error", "target host", err.Error())
	} else {
		add("ok", "target host", cfg.Routing.TargetHost)
	}

	if err := writable(filepath.Dir(cfg.State.Path)); err != nil {
		add("error", "state directory", err.Error())
	} else {
		add("ok", "state directory", cfg.State.Path)
	}

	client := caddy.New(cfg.Caddy.AdminURL)
	if err := client.Ping(ctx); err != nil {
		detail := err.Error()
		if extra := server.CaddyDiagnostics(); extra != "" {
			detail += "\n" + extra
		}
		add("error", "caddy admin", detail)
	} else {
		add("ok", "caddy admin", cfg.Caddy.AdminURL)
		ok, err := client.HasDomain(ctx, cfg.Server.BaseDomain)
		switch {
		case err != nil:
			add("error", "preview site", err.Error())
		case !ok:
			add("error", "preview site", fmt.Sprintf("Caddy is not serving *.%s; run: sudo preview server apply", cfg.Server.BaseDomain))
		default:
			add("ok", "preview site", "*."+cfg.Server.BaseDomain)
		}
	}

	if err := server.CheckDNSModule(cfg.Caddy.Bin, cfg.DNS.Provider); err != nil {
		add("error", "dns module", err.Error())
	} else {
		add("ok", "dns module", server.ModuleID(cfg.DNS.Provider))
	}

	if strings.TrimSpace(os.Getenv(cfg.DNS.CredentialsEnv)) == "" && !server.EnvFileHasValue(server.DNSEnvPath(configPath), cfg.DNS.CredentialsEnv) {
		add("warning", "dns credentials", fmt.Sprintf("%s is empty; set it in %s", cfg.DNS.CredentialsEnv, server.DNSEnvPath(configPath)))
	} else {
		add("ok", "dns credentials", cfg.DNS.CredentialsEnv+" is set")
	}

	addDNS(ctx, cfg, add)
	addCert(ctx, cfg, add)

	report := Report{Checks: checks, OK: true}
	for _, check := range checks {
		if check.Status == "error" {
			report.OK = false
			break
		}
	}
	return report
}

func addDNS(ctx context.Context, cfg config.Config, add func(string, string, string)) {
	if cfg.Server.BaseDomain == "" {
		return
	}
	name := "preview-check." + cfg.Server.BaseDomain
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ips, err := health.LookupHost(ctx, name)
	if err != nil {
		add("warning", "wildcard dns", fmt.Sprintf("%s does not resolve yet (%v). Add an A record for *.%s", name, err, cfg.Server.BaseDomain))
		return
	}
	if cfg.Server.PublicIP != "" && !containsIP(ips, cfg.Server.PublicIP) {
		add("error", "wildcard dns", fmt.Sprintf("%s resolves to %s, expected %s", name, strings.Join(ips, ", "), cfg.Server.PublicIP))
		return
	}
	if cfg.Server.PublicIPv6 != "" && !containsIP(ips, cfg.Server.PublicIPv6) {
		add("warning", "wildcard dns", fmt.Sprintf("%s resolves to %s, expected IPv6 %s", name, strings.Join(ips, ", "), cfg.Server.PublicIPv6))
		return
	}
	if cfg.Server.PublicIP == "" {
		add("warning", "wildcard dns", fmt.Sprintf("%s resolves to %s; set server.public_ip to confirm it is this machine", name, strings.Join(ips, ", ")))
		return
	}
	add("ok", "wildcard dns", fmt.Sprintf("%s resolves to %s", name, strings.Join(ips, ", ")))
}

func addCert(ctx context.Context, cfg config.Config, add func(string, string, string)) {
	if cfg.Server.BaseDomain == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	names, err := health.CertificateNames(ctx, "preview-check."+cfg.Server.BaseDomain, 5*time.Second)
	if err != nil {
		add("warning", "certificate", "wildcard certificate is not ready yet: "+err.Error())
		return
	}
	want := "*." + cfg.Server.BaseDomain
	for _, name := range names {
		if name == want || name == cfg.Server.BaseDomain {
			add("ok", "certificate", "covers "+want)
			return
		}
	}
	add("warning", "certificate", "presented "+strings.Join(names, ", "))
}

func writable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".preview-write-*")
	if err != nil {
		return fmt.Errorf("%s is not writable: %w", dir, err)
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}

func containsIP(addrs []string, want string) bool {
	target := net.ParseIP(want)
	for _, addr := range addrs {
		if addr == want {
			return true
		}
		if target != nil && target.Equal(net.ParseIP(addr)) {
			return true
		}
	}
	return false
}

// Counts returns error and warning totals.
func (r Report) Counts() (errors, warnings int) {
	for _, check := range r.Checks {
		switch check.Status {
		case "error":
			errors++
		case "warning":
			warnings++
		}
	}
	return errors, warnings
}
