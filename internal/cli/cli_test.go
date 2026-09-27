package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/harshalranjhani/preview-cli/internal/config"
)

func TestVersionAndRender(t *testing.T) {
	t.Cleanup(func() {
		flagConfig = ""
		flagJSON = false
		flagDebug = false
	})
	root := newRoot()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "preview") {
		t.Fatal(out.String())
	}

	dir := t.TempDir()
	cfg := config.Default()
	cfg.Server.BaseDomain = "preview.example.com"
	cfg.Server.PublicIP = "203.0.113.10"
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(config.Format(cfg)), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	root = newRoot()
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"--config", path, "server", "render"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "*.preview.example.com") {
		t.Fatal(out.String())
	}
	if !strings.Contains(out.String(), "DO NOT EDIT MANUALLY") {
		t.Fatal(out.String())
	}
}
