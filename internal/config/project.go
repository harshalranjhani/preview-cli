package config

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/harshalranjhani/preview-cli/internal/hostname"
	"gopkg.in/yaml.v3"
)

// ProjectFile is the optional repository-local .preview.yaml.
// It must not contain server credentials.
type ProjectFile struct {
	Project           string `yaml:"project"`
	DefaultNamePrefix string `yaml:"default_name_prefix"`
	DefaultTTL        string `yaml:"default_ttl"`
	Path              string `yaml:"-"`
}

// FindProject walks from cwd up to the git root looking for .preview.yaml.
func FindProject(cwd string) (ProjectFile, error) {
	gitRoot, _ := GitRoot(cwd)
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".preview.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return ParseProject(candidate)
		}
		if gitRoot != "" && dir == gitRoot {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ProjectFile{}, nil
}

// ParseProject reads a .preview.yaml file.
func ParseProject(path string) (ProjectFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ProjectFile{}, fmt.Errorf("read %s: %w", path, err)
	}
	var pf ProjectFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&pf); err != nil {
		return ProjectFile{}, fmt.Errorf("parse %s: %w", path, err)
	}
	pf.Path = path
	if pf.DefaultTTL != "" {
		if _, _, err := ParseTTL(pf.DefaultTTL); err != nil {
			return ProjectFile{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	return pf, nil
}

// WriteProject creates a small .preview.yaml.
func WriteProject(path string, pf ProjectFile) error {
	var b strings.Builder
	b.WriteString("# Local preview settings. Safe to commit. Do not put secrets here.\n")
	fmt.Fprintf(&b, "project: %s\n", yamlScalar(pf.Project))
	if pf.DefaultNamePrefix != "" {
		fmt.Fprintf(&b, "default_name_prefix: %s\n", yamlScalar(pf.DefaultNamePrefix))
	}
	if pf.DefaultTTL != "" {
		fmt.Fprintf(&b, "default_ttl: %s\n", yamlScalar(pf.DefaultTTL))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// ResolveProjectName picks a DNS-safe project slug.
// Order: flag, PREVIEW_PROJECT, .preview.yaml, git root directory, current directory.
func ResolveProjectName(flag string, pf ProjectFile, cwd string) (string, string, error) {
	gitRoot, _ := GitRoot(cwd)
	var raw string
	switch {
	case strings.TrimSpace(flag) != "":
		raw = flag
	case strings.TrimSpace(os.Getenv("PREVIEW_PROJECT")) != "":
		raw = os.Getenv("PREVIEW_PROJECT")
	case strings.TrimSpace(pf.Project) != "":
		raw = pf.Project
	case gitRoot != "":
		raw = filepath.Base(gitRoot)
	default:
		raw = filepath.Base(cwd)
	}
	slug := hostname.Slug(raw)
	if slug == "" {
		return "", gitRoot, errors.New("could not derive a DNS-safe project name; set it with --project or .preview.yaml")
	}
	return slug, gitRoot, nil
}

// ResolveTTL applies flag, environment, project file, then global config.
func ResolveTTL(flag, projectTTL string, cfg Config) (string, error) {
	candidates := []string{
		strings.TrimSpace(flag),
		strings.TrimSpace(os.Getenv("PREVIEW_DEFAULT_TTL")),
		strings.TrimSpace(projectTTL),
		strings.TrimSpace(cfg.Preview.DefaultTTL),
		"2h",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, _, err := ParseTTL(c); err != nil {
			return "", err
		}
		return c, nil
	}
	return "2h", nil
}

// ResolveName returns the DNS-safe preview name. An empty result is valid.
func ResolveName(flag, prefix string) (string, error) {
	raw := strings.TrimSpace(flag)
	if raw == "" {
		raw = strings.TrimSpace(prefix)
	}
	if raw == "" {
		return "", nil
	}
	slug := hostname.Slug(raw)
	if slug == "" {
		return "", fmt.Errorf("name %q is not a usable DNS label", raw)
	}
	return slug, nil
}

// GitRoot returns the repository root containing cwd, or "" when cwd is not a repo.
func GitRoot(cwd string) (string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ProjectDir returns the git root when present, otherwise cwd.
func ProjectDir(cwd string) string {
	if root, err := GitRoot(cwd); err == nil && root != "" {
		return root
	}
	return cwd
}
