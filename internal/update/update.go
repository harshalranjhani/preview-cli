package update

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const defaultRepo = "harshalranjhani/preview-cli"

// Result describes an update check.
type Result struct {
	Updated bool
	From    string
	To      string
	Path    string
}

// Options controls where the release is downloaded and which binary is replaced.
type Options struct {
	Current string
	Repo    string
	GOOS    string
	GOARCH  string
	Dest    string
	BaseURL string
	Client  *http.Client
}

// Run downloads the latest release and replaces Dest when it is older.
func Run(opts Options) (Result, error) {
	if opts.Repo == "" {
		opts.Repo = defaultRepo
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.GOARCH == "" {
		opts.GOARCH = runtime.GOARCH
	}
	if opts.BaseURL == "" {
		opts.BaseURL = "https://github.com"
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	if opts.Dest == "" {
		exe, err := os.Executable()
		if err != nil {
			return Result{}, err
		}
		opts.Dest = exe
	}
	asset, err := AssetName(opts.GOOS, opts.GOARCH)
	if err != nil {
		return Result{}, err
	}
	tag, err := latestTag(opts)
	if err != nil {
		return Result{}, err
	}
	latest := strings.TrimPrefix(tag, "v")
	current := strings.TrimPrefix(strings.TrimSpace(opts.Current), "v")
	result := Result{From: current, To: latest, Path: opts.Dest}
	if current != "" && current != "dev" && current == latest {
		return result, nil
	}
	url := strings.TrimRight(opts.BaseURL, "/") + "/" + opts.Repo + "/releases/download/" + tag + "/" + asset
	if err := install(opts.Client, url, opts.Dest); err != nil {
		return Result{}, err
	}
	result.Updated = true
	return result, nil
}

// AssetName is the release filename for an OS and architecture.
func AssetName(goos, goarch string) (string, error) {
	if goos != "linux" {
		return "", fmt.Errorf("release binaries are built for Linux; on this machine build from source with: go build -o preview ./cmd/preview")
	}
	switch goarch {
	case "amd64", "arm64":
		return "preview_" + goos + "_" + goarch, nil
	default:
		return "", fmt.Errorf("unsupported architecture %s", goarch)
	}
}

func latestTag(opts Options) (string, error) {
	client := &http.Client{
		Timeout: opts.Client.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	url := strings.TrimRight(opts.BaseURL, "/") + "/" + opts.Repo + "/releases/latest"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "preview")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("check latest release: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("check latest release: %s", resp.Status)
	}
	loc := resp.Header.Get("Location")
	tag := path.Base(loc)
	if !strings.HasPrefix(tag, "v") {
		return "", fmt.Errorf("latest release URL %q has no version", loc)
	}
	return tag, nil
}

func install(client *http.Client, url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "preview")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".preview-update-*")
	if err != nil {
		return fmt.Errorf("replace %s: %w\n\nRun: sudo preview update", dest, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, 100<<20)); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	info, err := os.Stat(tmpName)
	if err != nil {
		return err
	}
	if info.Size() < 4 {
		return fmt.Errorf("download was empty")
	}
	f, err := os.Open(tmpName)
	if err != nil {
		return err
	}
	magic := make([]byte, 4)
	_, _ = io.ReadFull(f, magic)
	f.Close()
	if string(magic) != "\x7fELF" {
		return fmt.Errorf("download was not a Linux binary")
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return fmt.Errorf("replace %s: %w\n\nRun: sudo preview update", dest, err)
	}
	return nil
}
