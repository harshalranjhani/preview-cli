package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/hostname"
)

func TestParseTTL(t *testing.T) {
	d, never, err := ParseTTL("2h")
	if err != nil || never || d != 2*time.Hour {
		t.Fatalf("2h => %s %v %v", d, never, err)
	}
	d, never, err = ParseTTL("1d")
	if err != nil || never || d != 24*time.Hour {
		t.Fatalf("1d => %s %v %v", d, never, err)
	}
	_, never, err = ParseTTL("0")
	if err != nil || !never {
		t.Fatalf("0 => never %v err %v", never, err)
	}
	if _, _, err := ParseTTL("soon"); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadMinimalAndEnv(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte("server:\n  base_domain: preview.example.com\n  public_ip: 203.0.113.10\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.BaseDomain != "preview.example.com" {
		t.Fatal(cfg.Server.BaseDomain)
	}
	if cfg.Caddy.AdminURL != "http://127.0.0.1:2019" {
		t.Fatal(cfg.Caddy.AdminURL)
	}
	if cfg.Preview.DefaultTTL != "2h" || !cfg.VerifyPublic() || cfg.AllowNonLoopback() {
		t.Fatalf("defaults not applied: %+v", cfg.Preview)
	}

	t.Setenv("PREVIEW_BASE_DOMAIN", "dev.example.com")
	t.Setenv("PREVIEW_PUBLIC_IP", "198.51.100.8")
	cfg, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.BaseDomain != "dev.example.com" || cfg.Server.PublicIP != "198.51.100.8" {
		t.Fatalf("env override failed: %+v", cfg.Server)
	}
}

func TestFormatRoundTrip(t *testing.T) {
	clearEnv(t)
	cfg := Default()
	cfg.Server.BaseDomain = "preview.example.com"
	cfg.Server.PublicIP = "203.0.113.10"
	cfg.Server.ACMEEmail = "ops@example.com"
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(Format(cfg)), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Server.BaseDomain != cfg.Server.BaseDomain || loaded.Server.HostnameTemplate != cfg.Server.HostnameTemplate {
		t.Fatalf("%+v", loaded.Server)
	}
	if loaded.DNS.Provider != "cloudflare" || loaded.State.Path != cfg.State.Path {
		t.Fatalf("%+v", loaded)
	}
}

func TestRejectsPublicAdminAndRemoteTargets(t *testing.T) {
	cfg := Default()
	cfg.Server.BaseDomain = "preview.example.com"
	cfg.Caddy.AdminURL = "http://203.0.113.10:2019"
	if err := cfg.Validate(); err == nil {
		t.Fatal("public admin URL should fail")
	}
	cfg = Default()
	cfg.Server.BaseDomain = "preview.example.com"
	cfg.Routing.TargetHost = "10.0.0.8"
	if err := cfg.Validate(); err == nil {
		t.Fatal("remote target should fail")
	}
	if err := ValidateTarget("127.0.0.1", false); err != nil {
		t.Fatal(err)
	}
}

func TestResolveProjectAndTTL(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	name, root, err := ResolveProjectName("", ProjectFile{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if root != "" {
		t.Fatalf("unexpected git root %s", root)
	}
	if name != hostname.Slug(filepath.Base(dir)) {
		t.Fatalf("name %s", name)
	}
	t.Setenv("PREVIEW_PROJECT", "From Env")
	name, _, err = ResolveProjectName("", ProjectFile{Project: "file"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "from-env" {
		t.Fatal(name)
	}
	name, _, err = ResolveProjectName("Flagged", ProjectFile{Project: "file"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if name != "flagged" {
		t.Fatal(name)
	}

	cfg := Default()
	ttl, err := ResolveTTL("", "30m", cfg)
	if err != nil || ttl != "30m" {
		t.Fatalf("project ttl %s %v", ttl, err)
	}
	t.Setenv("PREVIEW_DEFAULT_TTL", "45m")
	ttl, err = ResolveTTL("", "30m", cfg)
	if err != nil || ttl != "45m" {
		t.Fatalf("env ttl %s %v", ttl, err)
	}
	ttl, err = ResolveTTL("1h", "30m", cfg)
	if err != nil || ttl != "1h" {
		t.Fatalf("flag ttl %s %v", ttl, err)
	}
}

func TestProjectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".preview.yaml")
	if err := WriteProject(path, ProjectFile{Project: "dashboard", DefaultTTL: "1h"}); err != nil {
		t.Fatal(err)
	}
	pf, err := ParseProject(path)
	if err != nil {
		t.Fatal(err)
	}
	if pf.Project != "dashboard" || pf.DefaultTTL != "1h" {
		t.Fatalf("%+v", pf)
	}
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PREVIEW_CONFIG",
		"PREVIEW_BASE_DOMAIN",
		"PREVIEW_CADDY_ADMIN_URL",
		"PREVIEW_PUBLIC_IP",
		"PREVIEW_STATE_PATH",
		"PREVIEW_PROJECT",
		"PREVIEW_DEFAULT_TTL",
	} {
		t.Setenv(key, "")
	}
}
