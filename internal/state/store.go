package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Preview is one published route.
type Preview struct {
	ID           string     `json:"id"`
	Project      string     `json:"project"`
	Name         string     `json:"name,omitempty"`
	Hostname     string     `json:"hostname"`
	TargetHost   string     `json:"target_host"`
	TargetPort   int        `json:"target_port"`
	CreatedAt    time.Time  `json:"created_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	CreatedByUID int        `json:"created_by_uid"`
	CWD          string     `json:"cwd,omitempty"`
	GitRoot      string     `json:"git_root,omitempty"`
	CaddyRouteID string     `json:"caddy_route_id"`
}

// File is the on-disk state document.
type File struct {
	Previews []Preview `json:"previews"`
}

// Store is a JSON file guarded by an exclusive file lock.
type Store struct {
	Path string
}

func New(path string) *Store {
	return &Store{Path: path}
}

// Update locks the state file, lets fn mutate it, and writes atomically.
// If fn returns an error the file is left unchanged.
func (s *Store) Update(fn func(*File) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()

	file, err := s.read()
	if err != nil {
		return err
	}
	if err := fn(&file); err != nil {
		return err
	}
	return s.write(file)
}

// Read returns a copy of the current state.
func (s *Store) Read() (File, error) {
	unlock, err := s.lock()
	if err != nil {
		return File{}, err
	}
	defer unlock()
	return s.read()
}

func (s *Store) read() (File, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{Previews: []Preview{}}, nil
		}
		return File{}, fmt.Errorf("read state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return File{Previews: []Preview{}}, nil
	}
	var file File
	if err := json.Unmarshal(data, &file); err != nil {
		return File{}, fmt.Errorf("parse state: %w", err)
	}
	if file.Previews == nil {
		file.Previews = []Preview{}
	}
	return file, nil
}

func (s *Store) write(file File) error {
	if file.Previews == nil {
		file.Previews = []Preview{}
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	if err := os.Rename(tmp, s.Path); err != nil {
		return fmt.Errorf("replace state: %w", err)
	}
	return nil
}

func (s *Store) lock() (func(), error) {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock state: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

// Find returns the preview identified by an id or hostname.
func (f File) Find(ref string) (Preview, bool) {
	ref = normalizeRef(ref)
	for _, p := range f.Previews {
		if p.ID == ref || p.Hostname == ref || p.CaddyRouteID == ref {
			return p, true
		}
	}
	return Preview{}, false
}

// Put inserts or replaces a preview by id.
func (f *File) Put(p Preview) {
	for i := range f.Previews {
		if f.Previews[i].ID == p.ID {
			f.Previews[i] = p
			return
		}
	}
	f.Previews = append(f.Previews, p)
}

// Remove deletes a preview by id.
func (f *File) Remove(id string) bool {
	next := f.Previews[:0]
	removed := false
	for _, p := range f.Previews {
		if p.ID == id {
			removed = true
			continue
		}
		next = append(next, p)
	}
	f.Previews = next
	return removed
}

// HostnameTaken reports whether hostname is already published.
func (f File) HostnameTaken(hostname string) bool {
	for _, p := range f.Previews {
		if p.Hostname == hostname {
			return true
		}
	}
	return false
}

// IDTaken reports whether id is already used.
func (f File) IDTaken(id string) bool {
	for _, p := range f.Previews {
		if p.ID == id {
			return true
		}
	}
	return false
}

func normalizeRef(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "https://")
	ref = strings.TrimPrefix(ref, "http://")
	ref = strings.TrimSuffix(ref, "/")
	if i := strings.IndexByte(ref, '/'); i >= 0 {
		ref = ref[:i]
	}
	return ref
}

// Expired reports whether the preview lifetime has elapsed.
func (p Preview) Expired(now time.Time) bool {
	return p.ExpiresAt != nil && !p.ExpiresAt.After(now)
}

// TargetURL is the local address Caddy proxies to.
func (p Preview) TargetURL() string {
	return fmt.Sprintf("http://%s", p.Dial())
}

// Dial is the host:port used by the reverse proxy.
func (p Preview) Dial() string {
	return netJoin(p.TargetHost, p.TargetPort)
}

func netJoin(host string, port int) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return fmt.Sprintf("[%s]:%d", host, port)
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// PublicURL is the HTTPS preview URL.
func (p Preview) PublicURL() string {
	return "https://" + p.Hostname
}
