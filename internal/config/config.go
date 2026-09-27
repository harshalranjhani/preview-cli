package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const DefaultPath = "/etc/preview/config.yaml"

// ErrNotFound means no configuration file exists at the resolved path.
var ErrNotFound = errors.New("config file not found")

// Config is the server-wide configuration. Secrets never belong here.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	DNS      DNSConfig      `yaml:"dns"`
	Caddy    CaddyConfig    `yaml:"caddy"`
	Routing  RoutingConfig  `yaml:"routing"`
	Preview  PreviewConfig  `yaml:"preview"`
	State    StateConfig    `yaml:"state"`
	Security SecurityConfig `yaml:"security"`
}

type ServerConfig struct {
	BaseDomain       string `yaml:"base_domain"`
	HostnameTemplate string `yaml:"hostname_template"`
	PublicIP         string `yaml:"public_ip"`
	PublicIPv6       string `yaml:"public_ipv6"`
	ACMEEmail        string `yaml:"acme_email"`
}

type DNSConfig struct {
	Provider       string `yaml:"provider"`
	CredentialsEnv string `yaml:"credentials_env"`
}

type CaddyConfig struct {
	AdminURL          string `yaml:"admin_url"`
	ManagedConfigPath string `yaml:"managed_config_path"`
	MainConfigPath    string `yaml:"main_config_path"`
	ServerName        string `yaml:"server_name"`
	Bin               string `yaml:"bin"`
}

type RoutingConfig struct {
	TargetHost     string `yaml:"target_host"`
	AllowedPortMin int    `yaml:"allowed_port_min"`
	AllowedPortMax int    `yaml:"allowed_port_max"`
}

type PreviewConfig struct {
	DefaultTTL      string        `yaml:"default_ttl"`
	HealthTimeout   string        `yaml:"health_timeout"`
	VerifyPublicURL *bool         `yaml:"verify_public_url"`
	StaleAfter      string        `yaml:"stale_after"`
	healthTimeout   time.Duration `yaml:"-"`
	staleAfter      time.Duration `yaml:"-"`
}

type StateConfig struct {
	Path string `yaml:"path"`
}

type SecurityConfig struct {
	AllowNonLoopbackTargets *bool `yaml:"allow_non_loopback_targets"`
}

// Default returns built-in settings. Base domain is intentionally blank.
func Default() Config {
	verify := true
	allow := false
	return Config{
		Server: ServerConfig{
			HostnameTemplate: "{{project}}--{{name}}--{{id}}.{{base_domain}}",
		},
		DNS: DNSConfig{
			Provider:       "cloudflare",
			CredentialsEnv: "CLOUDFLARE_API_TOKEN",
		},
		Caddy: CaddyConfig{
			AdminURL:          "http://127.0.0.1:2019",
			ManagedConfigPath: "/etc/caddy/preview.caddy",
			MainConfigPath:    "/etc/caddy/Caddyfile",
			Bin:               "caddy",
		},
		Routing: RoutingConfig{
			TargetHost:     "127.0.0.1",
			AllowedPortMin: 1024,
			AllowedPortMax: 65535,
		},
		Preview: PreviewConfig{
			DefaultTTL:      "2h",
			HealthTimeout:   "15s",
			VerifyPublicURL: &verify,
			StaleAfter:      "10m",
		},
		State: StateConfig{
			Path: "/var/lib/preview/state.json",
		},
		Security: SecurityConfig{
			AllowNonLoopbackTargets: &allow,
		},
	}
}

// ResolvePath chooses the config file from a flag, PREVIEW_CONFIG, or the default path.
func ResolvePath(flag string) string {
	if strings.TrimSpace(flag) != "" {
		return flag
	}
	if v := strings.TrimSpace(os.Getenv("PREVIEW_CONFIG")); v != "" {
		return v
	}
	return DefaultPath
}

// Load reads, defaults, applies environment overrides, and validates the file.
func Load(path string) (Config, error) {
	cfg, err := LoadLoose(path)
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadLoose reads a config file without requiring a base domain.
// A missing file returns ErrNotFound.
func LoadLoose(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := Default()
	if len(strings.TrimSpace(string(data))) > 0 {
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
			return Config{}, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	cfg.ApplyDefaults()
	cfg.ApplyEnv()
	if err := cfg.prepare(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// ApplyDefaults fills blank fields. Explicit false values are preserved.
func (c *Config) ApplyDefaults() {
	d := Default()
	if c.Server.HostnameTemplate == "" {
		c.Server.HostnameTemplate = d.Server.HostnameTemplate
	}
	if c.DNS.Provider == "" {
		c.DNS.Provider = d.DNS.Provider
	}
	if c.DNS.CredentialsEnv == "" {
		c.DNS.CredentialsEnv = d.DNS.CredentialsEnv
	}
	if c.Caddy.AdminURL == "" {
		c.Caddy.AdminURL = d.Caddy.AdminURL
	}
	if c.Caddy.ManagedConfigPath == "" {
		c.Caddy.ManagedConfigPath = d.Caddy.ManagedConfigPath
	}
	if c.Caddy.MainConfigPath == "" {
		c.Caddy.MainConfigPath = d.Caddy.MainConfigPath
	}
	if c.Caddy.Bin == "" {
		c.Caddy.Bin = d.Caddy.Bin
	}
	if c.Routing.TargetHost == "" {
		c.Routing.TargetHost = d.Routing.TargetHost
	}
	if c.Routing.AllowedPortMin == 0 {
		c.Routing.AllowedPortMin = d.Routing.AllowedPortMin
	}
	if c.Routing.AllowedPortMax == 0 {
		c.Routing.AllowedPortMax = d.Routing.AllowedPortMax
	}
	if c.Preview.DefaultTTL == "" {
		c.Preview.DefaultTTL = d.Preview.DefaultTTL
	}
	if c.Preview.HealthTimeout == "" {
		c.Preview.HealthTimeout = d.Preview.HealthTimeout
	}
	if c.Preview.VerifyPublicURL == nil {
		c.Preview.VerifyPublicURL = d.Preview.VerifyPublicURL
	}
	if c.Preview.StaleAfter == "" {
		c.Preview.StaleAfter = d.Preview.StaleAfter
	}
	if c.State.Path == "" {
		c.State.Path = d.State.Path
	}
	if c.Security.AllowNonLoopbackTargets == nil {
		c.Security.AllowNonLoopbackTargets = d.Security.AllowNonLoopbackTargets
	}
	c.Server.BaseDomain = normalizeDomain(c.Server.BaseDomain)
}

// ApplyEnv applies environment overrides. Flags are applied by callers after this.
func (c *Config) ApplyEnv() {
	if v := strings.TrimSpace(os.Getenv("PREVIEW_BASE_DOMAIN")); v != "" {
		c.Server.BaseDomain = normalizeDomain(v)
	}
	if v := strings.TrimSpace(os.Getenv("PREVIEW_CADDY_ADMIN_URL")); v != "" {
		c.Caddy.AdminURL = v
	}
	if v := strings.TrimSpace(os.Getenv("PREVIEW_PUBLIC_IP")); v != "" {
		c.Server.PublicIP = v
	}
	if v := strings.TrimSpace(os.Getenv("PREVIEW_STATE_PATH")); v != "" {
		c.State.Path = v
	}
}

func (c *Config) prepare() error {
	c.Server.BaseDomain = normalizeDomain(c.Server.BaseDomain)
	timeout, err := time.ParseDuration(c.Preview.HealthTimeout)
	if err != nil {
		return fmt.Errorf("preview.health_timeout: %w", err)
	}
	c.Preview.healthTimeout = timeout
	stale, err := time.ParseDuration(c.Preview.StaleAfter)
	if err != nil {
		return fmt.Errorf("preview.stale_after: %w", err)
	}
	c.Preview.staleAfter = stale
	if _, _, err := ParseTTL(c.Preview.DefaultTTL); err != nil {
		return fmt.Errorf("preview.default_ttl: %w", err)
	}
	return nil
}

// Validate checks that the server configuration is safe to use.
func (c Config) Validate() error {
	if c.Server.BaseDomain == "" {
		return errors.New("server.base_domain is required")
	}
	if strings.Contains(c.Server.BaseDomain, "*") {
		return errors.New("server.base_domain must not contain a wildcard; use preview.example.com so the tool can serve *.preview.example.com")
	}
	if err := c.prepare(); err != nil {
		return err
	}
	if err := validateAdminURL(c.Caddy.AdminURL); err != nil {
		return err
	}
	if c.Server.PublicIP != "" && net.ParseIP(c.Server.PublicIP) == nil {
		return fmt.Errorf("server.public_ip %q is not an IP address", c.Server.PublicIP)
	}
	if c.Server.PublicIPv6 != "" && net.ParseIP(c.Server.PublicIPv6) == nil {
		return fmt.Errorf("server.public_ipv6 %q is not an IP address", c.Server.PublicIPv6)
	}
	if c.Routing.AllowedPortMin < 1 || c.Routing.AllowedPortMax > 65535 || c.Routing.AllowedPortMin > c.Routing.AllowedPortMax {
		return fmt.Errorf("routing port range %d-%d is invalid", c.Routing.AllowedPortMin, c.Routing.AllowedPortMax)
	}
	if err := ValidateTarget(c.Routing.TargetHost, c.AllowNonLoopback()); err != nil {
		return err
	}
	if strings.TrimSpace(c.DNS.Provider) == "" {
		return errors.New("dns.provider is required")
	}
	if strings.TrimSpace(c.DNS.CredentialsEnv) == "" {
		return errors.New("dns.credentials_env is required")
	}
	return nil
}

// VerifyPublic reports whether new previews should be requested over HTTPS.
func (c Config) VerifyPublic() bool {
	return c.Preview.VerifyPublicURL != nil && *c.Preview.VerifyPublicURL
}

// AllowNonLoopback reports whether targets other than loopback are permitted.
func (c Config) AllowNonLoopback() bool {
	return c.Security.AllowNonLoopbackTargets != nil && *c.Security.AllowNonLoopbackTargets
}

// HealthTimeout is the parsed preview health check timeout.
func (c Config) HealthTimeout() time.Duration {
	if c.Preview.healthTimeout == 0 {
		d, err := time.ParseDuration(c.Preview.HealthTimeout)
		if err != nil {
			return 15 * time.Second
		}
		return d
	}
	return c.Preview.healthTimeout
}

// StaleAfter is how long a dead target must stay unreachable before gc removes it.
func (c Config) StaleAfter() time.Duration {
	if c.Preview.staleAfter == 0 {
		d, err := time.ParseDuration(c.Preview.StaleAfter)
		if err != nil {
			return 10 * time.Minute
		}
		return d
	}
	return c.Preview.staleAfter
}

// CheckPort reports whether port is inside the configured range.
func (c Config) CheckPort(port int) error {
	if port < c.Routing.AllowedPortMin || port > c.Routing.AllowedPortMax {
		return fmt.Errorf("port %d is outside the allowed range %d-%d", port, c.Routing.AllowedPortMin, c.Routing.AllowedPortMax)
	}
	return nil
}

// ValidateTarget rejects remote hosts unless the operator opted in.
func ValidateTarget(host string, allowNonLoopback bool) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return errors.New("target host is empty")
	}
	if strings.Contains(host, "://") || strings.Contains(host, "/") {
		return fmt.Errorf("target host %q must be a host, not a URL", host)
	}
	if allowNonLoopback {
		return nil
	}
	if isLoopback(host) {
		return nil
	}
	return fmt.Errorf("target host %q is not loopback; previews only proxy to this machine unless security.allow_non_loopback_targets is true", host)
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAdminURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("caddy.admin_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("caddy.admin_url %q must be an http(s) URL", raw)
	}
	host := u.Hostname()
	if !isLoopback(host) {
		return fmt.Errorf("caddy.admin_url must stay on localhost, got %q", raw)
	}
	if u.Port() == "" {
		return fmt.Errorf("caddy.admin_url %q needs a port", raw)
	}
	return nil
}

func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(strings.ToLower(domain))
	domain = strings.TrimPrefix(domain, "*.")
	domain = strings.TrimPrefix(domain, ".")
	domain = strings.TrimSuffix(domain, ".")
	return domain
}

// ParseTTL parses a lifetime. "0" means the preview does not expire.
func ParseTTL(s string) (time.Duration, bool, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, false, errors.New("ttl is empty")
	}
	if s == "0" || s == "0s" || s == "none" || s == "never" {
		return 0, true, nil
	}
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n < 0 {
			return 0, false, fmt.Errorf("invalid ttl %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, false, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return 0, false, fmt.Errorf("invalid ttl %q (use 30m, 2h, 1d, or 0)", s)
	}
	return d, false, nil
}

// AdminListen returns the host:port Caddy should bind for its admin API.
func AdminListen(adminURL string) (string, error) {
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("admin URL %q has no host", adminURL)
	}
	return u.Host, nil
}

// Format writes a commented config file users can edit.
func Format(c Config) string {
	c.ApplyDefaults()
	verify := c.VerifyPublic()
	allow := c.AllowNonLoopback()
	var b strings.Builder
	b.WriteString("# preview server configuration.\n")
	b.WriteString("# Edit the values in this file, then run:\n")
	b.WriteString("#   sudo preview server apply\n")
	b.WriteString("#\n")
	b.WriteString("# Put DNS API tokens in the credentials env file, never in this file.\n")
	b.WriteString("\n")
	b.WriteString("server:\n")
	b.WriteString("  # Apex under the wildcard. DNS should have *.base_domain pointing at public_ip.\n")
	fmt.Fprintf(&b, "  base_domain: %s\n", yamlScalar(c.Server.BaseDomain))
	b.WriteString("  # Placeholders: {{project}} {{name}} {{id}} {{base_domain}}\n")
	fmt.Fprintf(&b, "  hostname_template: %s\n", yamlScalar(c.Server.HostnameTemplate))
	b.WriteString("  # Public address of this server. Used to check that DNS points here.\n")
	fmt.Fprintf(&b, "  public_ip: %s\n", yamlScalar(c.Server.PublicIP))
	fmt.Fprintf(&b, "  public_ipv6: %s\n", yamlScalar(c.Server.PublicIPv6))
	fmt.Fprintf(&b, "  acme_email: %s\n", yamlScalar(c.Server.ACMEEmail))
	b.WriteString("\n")
	b.WriteString("dns:\n")
	b.WriteString("  # Caddy DNS module used for the wildcard certificate. cloudflare is the usual choice.\n")
	fmt.Fprintf(&b, "  provider: %s\n", yamlScalar(c.DNS.Provider))
	fmt.Fprintf(&b, "  credentials_env: %s\n", yamlScalar(c.DNS.CredentialsEnv))
	b.WriteString("\n")
	b.WriteString("caddy:\n")
	fmt.Fprintf(&b, "  admin_url: %s\n", yamlScalar(c.Caddy.AdminURL))
	fmt.Fprintf(&b, "  managed_config_path: %s\n", yamlScalar(c.Caddy.ManagedConfigPath))
	fmt.Fprintf(&b, "  main_config_path: %s\n", yamlScalar(c.Caddy.MainConfigPath))
	fmt.Fprintf(&b, "  server_name: %s\n", yamlScalar(c.Caddy.ServerName))
	fmt.Fprintf(&b, "  bin: %s\n", yamlScalar(c.Caddy.Bin))
	b.WriteString("\n")
	b.WriteString("routing:\n")
	fmt.Fprintf(&b, "  target_host: %s\n", yamlScalar(c.Routing.TargetHost))
	fmt.Fprintf(&b, "  allowed_port_min: %d\n", c.Routing.AllowedPortMin)
	fmt.Fprintf(&b, "  allowed_port_max: %d\n", c.Routing.AllowedPortMax)
	b.WriteString("\n")
	b.WriteString("preview:\n")
	fmt.Fprintf(&b, "  default_ttl: %s\n", yamlScalar(c.Preview.DefaultTTL))
	fmt.Fprintf(&b, "  health_timeout: %s\n", yamlScalar(c.Preview.HealthTimeout))
	fmt.Fprintf(&b, "  verify_public_url: %t\n", verify)
	fmt.Fprintf(&b, "  stale_after: %s\n", yamlScalar(c.Preview.StaleAfter))
	b.WriteString("\n")
	b.WriteString("state:\n")
	fmt.Fprintf(&b, "  path: %s\n", yamlScalar(c.State.Path))
	b.WriteString("\n")
	b.WriteString("security:\n")
	fmt.Fprintf(&b, "  allow_non_loopback_targets: %t\n", allow)
	return b.String()
}

func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`\n\t ") || strings.HasPrefix(s, "*") {
		return strconv.Quote(s)
	}
	return s
}
