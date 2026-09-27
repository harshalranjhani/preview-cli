package hostname

import (
	"crypto/rand"
	"fmt"
	"strings"
)

const idAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// Slug turns an arbitrary name into a DNS-safe label fragment.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// Truncate shortens an ASCII slug to at most max bytes and trims dashes.
func Truncate(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if len(s) <= max {
		return strings.Trim(s, "-")
	}
	return strings.Trim(s[:max], "-")
}

// ShortID returns a 4-character id suitable for hostnames.
func ShortID() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = idAlphabet[int(buf[i])%len(idAlphabet)]
	}
	return string(buf), nil
}

// PreviewID is the public identifier, for example pv_k7p2.
func PreviewID(short string) string {
	return "pv_" + short
}

// RouteID is the stable Caddy object id for a preview.
func RouteID(previewID string) string {
	return "preview-" + strings.ReplaceAll(previewID, "_", "-")
}

// Validate reports whether host is a DNS hostname this CLI can publish.
func Validate(host string) error {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || len(host) > 253 {
		return fmt.Errorf("hostname length must be 1-253 characters")
	}
	if strings.ContainsAny(host, "/:\\@ ") {
		return fmt.Errorf("hostname %q contains invalid characters", host)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return fmt.Errorf("hostname %q needs a domain", host)
	}
	for _, label := range labels {
		if err := validateLabel(label); err != nil {
			return fmt.Errorf("hostname %q: %w", host, err)
		}
	}
	return nil
}

func validateLabel(label string) error {
	if label == "" || len(label) > 63 {
		return fmt.Errorf("label %q must be 1-63 characters", label)
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
		if !ok {
			return fmt.Errorf("label %q has an invalid character", label)
		}
		if c == '-' && (i == 0 || i == len(label)-1) {
			return fmt.Errorf("label %q cannot start or end with a dash", label)
		}
	}
	return nil
}

// Build expands a hostname template and shortens the project or name until it fits.
// id is the short id without the pv_ prefix.
func Build(tmpl, project, name, id, baseDomain string) (string, error) {
	if !strings.Contains(tmpl, "{{id}}") {
		return "", fmt.Errorf("hostname template must include {{id}}")
	}
	project = Slug(project)
	name = Slug(name)
	id = Slug(id)
	baseDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if project == "" {
		return "", fmt.Errorf("project name is empty after normalizing")
	}
	if id == "" {
		return "", fmt.Errorf("preview id is empty")
	}
	if baseDomain == "" {
		return "", fmt.Errorf("base domain is empty")
	}

	host := expand(tmpl, project, name, id, baseDomain)
	if err := Validate(host); err == nil {
		return host, nil
	}

	for length := 40; length >= 1; length-- {
		p := Truncate(project, length)
		n := name
		if name != "" {
			n = Truncate(name, length)
		}
		if p == "" {
			continue
		}
		host = expand(tmpl, p, n, id, baseDomain)
		if Validate(host) == nil {
			return host, nil
		}
	}
	return "", fmt.Errorf("hostname template produced an invalid name")
}

func expand(tmpl, project, name, id, base string) string {
	t := tmpl
	if name == "" {
		t = strings.ReplaceAll(t, "--{{name}}", "")
		t = strings.ReplaceAll(t, "{{name}}--", "")
		t = strings.ReplaceAll(t, ".{{name}}", "")
		t = strings.ReplaceAll(t, "{{name}}.", "")
		t = strings.ReplaceAll(t, "{{name}}", "")
	}
	out := strings.NewReplacer(
		"{{project}}", project,
		"{{name}}", name,
		"{{id}}", id,
		"{{base_domain}}", base,
	).Replace(t)
	out = strings.ToLower(strings.TrimSpace(out))
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	return strings.Trim(out, ".")
}

// CoversDomain reports whether a template will stay under *.baseDomain.
func CoversDomain(tmpl, baseDomain string) bool {
	baseDomain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(baseDomain)), ".")
	if baseDomain == "" {
		return false
	}
	if strings.Contains(tmpl, "{{base_domain}}") {
		return true
	}
	return strings.Contains(strings.ToLower(tmpl), "."+baseDomain) || strings.HasSuffix(strings.ToLower(tmpl), baseDomain)
}
