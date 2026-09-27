package server

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// InstallSystemd enables the garbage-collection timer, route restore service, and Caddy DNS env file.
// It returns a warning string when systemd is not available.
func InstallSystemd(binPath, dnsEnvPath string) (string, error) {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "systemd was not found; run preview gc yourself, or from cron, every few minutes", nil
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		return "systemd is not running; skip timer installation and run preview gc from cron", nil
	}
	units := map[string]string{
		"preview-gc.service":      strings.ReplaceAll(gcService, "@BIN@", binPath),
		"preview-gc.timer":        gcTimer,
		"preview-restore.service": strings.ReplaceAll(restoreService, "@BIN@", binPath),
	}
	dir := "/etc/systemd/system"
	for name, content := range units {
		if err := WriteAtomic(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return "", err
		}
	}
	dropinDir := "/etc/systemd/system/caddy.service.d"
	dropin := fmt.Sprintf("[Service]\nEnvironmentFile=-%s\nNoNewPrivileges=false\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n", dnsEnvPath)
	if err := WriteAtomic(filepath.Join(dropinDir, "preview-dns.conf"), []byte(dropin), 0o644); err != nil {
		return "", err
	}
	if out, err := exec.Command("systemctl", "daemon-reload").CombinedOutput(); err != nil {
		return "", fmt.Errorf("systemctl daemon-reload: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "enable", "--now", "preview-gc.timer").CombinedOutput(); err != nil {
		return "", fmt.Errorf("enable preview-gc.timer: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "enable", "preview-restore.service").CombinedOutput(); err != nil {
		return "", fmt.Errorf("enable preview-restore.service: %w\n%s", err, strings.TrimSpace(string(out)))
	}
	return "", nil
}

// Executable returns the path recorded in systemd units.
func Executable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path, nil
	}
	return resolved, nil
}

const gcService = `[Unit]
Description=Remove expired and stale preview routes
After=network-online.target

[Service]
Type=oneshot
ExecStart=@BIN@ gc
`

const gcTimer = `[Unit]
Description=Run preview garbage collection every 5 minutes

[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
AccuracySec=1min
Persistent=true

[Install]
WantedBy=timers.target
`

const restoreService = `[Unit]
Description=Restore preview routes into Caddy
After=caddy.service
Wants=caddy.service

[Service]
Type=oneshot
ExecStart=@BIN@ server sync
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
`

// UnitSources returns the unit file bodies with the binary path filled in.
func UnitSources(binPath string) map[string]string {
	return map[string]string{
		"preview-gc.service":      strings.ReplaceAll(gcService, "@BIN@", binPath),
		"preview-gc.timer":        gcTimer,
		"preview-restore.service": strings.ReplaceAll(restoreService, "@BIN@", binPath),
	}
}

// BytesContains is a small wrapper so callers can compare generated units in tests.
func BytesContains(content, part string) bool {
	return bytes.Contains([]byte(content), []byte(part))
}
