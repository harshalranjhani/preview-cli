package server

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/harshalranjhani/preview-cli/internal/config"
)

// UninstallOptions controls how much of the local install is removed.
type UninstallOptions struct {
	ConfigPath  string
	BinPath     string
	RemoveCaddy bool
}

// Uninstall removes preview routes, config, and the CLI. Caddy itself stays unless RemoveCaddy is set.
func Uninstall(opts UninstallOptions) ([]string, error) {
	var removed []string
	note := func(path string) {
		if path != "" {
			removed = append(removed, path)
		}
	}

	cfg := config.Default()
	if opts.ConfigPath != "" {
		if loaded, err := config.LoadLoose(opts.ConfigPath); err == nil {
			cfg = loaded
		}
	}

	runQuiet("systemctl", "disable", "--now", "preview-gc.timer", "preview-restore.service")
	for _, path := range []string{
		"/etc/systemd/system/preview-gc.service",
		"/etc/systemd/system/preview-gc.timer",
		"/etc/systemd/system/preview-restore.service",
		"/etc/systemd/system/caddy.service.d/preview-dns.conf",
		"/etc/systemd/system/caddy.service.d/preview-bind.conf",
	} {
		if err := os.Remove(path); err == nil {
			note(path)
		}
	}

	if err := removeManagedSite(cfg.Caddy.MainConfigPath, cfg.Caddy.ManagedConfigPath); err == nil {
		note(cfg.Caddy.ManagedConfigPath)
	}
	runQuiet("systemctl", "daemon-reload")
	if !opts.RemoveCaddy {
		runQuiet("systemctl", "reload", "caddy")
	}

	for _, path := range []string{filepath.Dir(opts.ConfigPath), filepath.Dir(cfg.State.Path)} {
		if path == "" || path == "." || path == "/" {
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			note(path)
		}
	}

	if opts.RemoveCaddy {
		runQuiet("systemctl", "disable", "--now", "caddy")
		for _, path := range []string{"/usr/bin/caddy", "/etc/systemd/system/caddy.service"} {
			if err := os.Remove(path); err == nil {
				note(path)
			}
		}
		runQuiet("systemctl", "daemon-reload")
	}

	if opts.BinPath != "" {
		if err := os.Remove(opts.BinPath); err == nil {
			note(opts.BinPath)
		}
	}
	if len(removed) == 0 {
		return nil, fmt.Errorf("nothing to remove")
	}
	return removed, nil
}

func removeManagedSite(mainPath, snippetPath string) error {
	if mainPath != "" {
		data, err := os.ReadFile(mainPath)
		if err == nil {
			next := StripManagedImport(string(data), snippetPath)
			if next != string(data) {
				if err := WriteAtomic(mainPath, []byte(next), 0o644); err != nil {
					return err
				}
			}
		}
	}
	if snippetPath == "" {
		return nil
	}
	return os.Remove(snippetPath)
}

// StripManagedImport removes the preview import from a Caddyfile.
func StripManagedImport(content, snippetPath string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, importMarker) {
			continue
		}
		fields := strings.Fields(trimmed)
		if snippetPath != "" && len(fields) >= 2 && fields[0] == "import" && fields[1] == snippetPath {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, "\n")
	if strings.HasSuffix(content, "\n") && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	return out
}

func runQuiet(name string, args ...string) {
	if _, err := exec.LookPath(name); err != nil {
		return
	}
	_ = exec.Command(name, args...).Run()
}
