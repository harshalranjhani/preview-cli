package update

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	name, err := AssetName("linux", "amd64")
	if err != nil || name != "preview_linux_amd64" {
		t.Fatalf("%s %v", name, err)
	}
	if _, err := AssetName("darwin", "arm64"); err == nil {
		t.Fatal("expected non-linux to fail")
	}
}

func TestRunReplacesBinary(t *testing.T) {
	elf := append([]byte{0x7f, 'E', 'L', 'F'}, []byte("preview-binary")...)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			http.Redirect(w, r, srv.URL+"/harshalranjhani/preview-cli/releases/tag/v0.1.1", http.StatusFound)
		case strings.Contains(r.URL.Path, "/releases/download/v0.1.1/preview_linux_amd64"):
			_, _ = w.Write(elf)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	dest := filepath.Join(dir, "preview")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(Options{
		Current: "0.1.0",
		GOOS:    "linux",
		GOARCH:  "amd64",
		Dest:    dest,
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.To != "0.1.1" {
		t.Fatalf("%+v", result)
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != string(elf) {
		t.Fatalf("%q %v", got, err)
	}
}

func TestRunSkipsCurrentVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://github.com/harshalranjhani/preview-cli/releases/tag/v0.1.1", http.StatusFound)
	}))
	defer srv.Close()
	result, err := Run(Options{
		Current: "v0.1.1",
		GOOS:    "linux",
		GOARCH:  "amd64",
		Dest:    filepath.Join(t.TempDir(), "preview"),
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Updated {
		t.Fatal("already current")
	}
}
