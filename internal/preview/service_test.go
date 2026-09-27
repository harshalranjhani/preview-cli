package preview

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/caddy"
	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/state"
)

type fakeCaddy struct {
	mu     sync.Mutex
	routes map[string]json.RawMessage
}

func newFakeCaddy(t *testing.T) (*fakeCaddy, *httptest.Server) {
	t.Helper()
	f := &fakeCaddy{routes: map[string]json.RawMessage{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeCaddy) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		_ = json.NewEncoder(w).Encode(f.snapshot())
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/id/"):
		raw, ok := f.routes[routeID(r.URL.Path)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/id/"):
		id := routeID(r.URL.Path)
		if _, ok := f.routes[id]; !ok {
			http.NotFound(w, r)
			return
		}
		delete(f.routes, id)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/routes/"):
		body, _ := io.ReadAll(r.Body)
		var meta struct {
			ID string `json:"@id"`
		}
		if err := json.Unmarshal(body, &meta); err != nil || meta.ID == "" {
			http.Error(w, "missing id", http.StatusBadRequest)
			return
		}
		f.routes[meta.ID] = append(json.RawMessage(nil), body...)
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCaddy) snapshot() map[string]any {
	routes := []any{
		map[string]any{
			"match": []any{map[string]any{"host": []any{"*.preview.example.com"}}},
		},
	}
	for _, raw := range f.routes {
		var decoded any
		_ = json.Unmarshal(raw, &decoded)
		routes = append(routes, decoded)
	}
	return map[string]any{
		"apps": map[string]any{
			"http": map[string]any{
				"servers": map[string]any{
					"srv0": map[string]any{"routes": routes},
				},
			},
		},
	}
}

func (f *fakeCaddy) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.routes)
}

func routeID(path string) string {
	id := strings.TrimPrefix(path, "/id/")
	decoded, err := url.PathUnescape(id)
	if err != nil {
		return id
	}
	return decoded
}

func testDeps(t *testing.T, admin string, verify bool) Deps {
	t.Helper()
	cfg := config.Default()
	cfg.Server.BaseDomain = "preview.example.com"
	cfg.Server.PublicIP = "203.0.113.10"
	cfg.Caddy.AdminURL = admin
	cfg.State.Path = filepath.Join(t.TempDir(), "state.json")
	cfg.Preview.VerifyPublicURL = &verify
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	return Deps{
		Config: cfg,
		Store:  state.New(cfg.State.Path),
		Caddy:  caddy.New(admin),
		CWD:    t.TempDir(),
	}
}

func listenLocal(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestCreateStopAndSharedPort(t *testing.T) {
	fake, srv := newFakeCaddy(t)
	deps := testDeps(t, srv.URL, false)
	port := listenLocal(t)
	ctx := context.Background()

	first, err := Create(ctx, deps, CreateInput{Port: port, Name: "auth fix", Project: "Dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Preview.Project != "dashboard" || first.Preview.Name != "auth-fix" {
		t.Fatalf("%+v", first.Preview)
	}
	if !strings.Contains(first.Preview.Hostname, "dashboard--auth-fix--") || !strings.HasSuffix(first.Preview.Hostname, ".preview.example.com") {
		t.Fatal(first.Preview.Hostname)
	}
	if first.View["url"] != first.Preview.PublicURL() {
		t.Fatalf("url %v", first.View["url"])
	}
	if fake.count() != 1 {
		t.Fatalf("routes %d", fake.count())
	}

	second, err := Create(ctx, deps, CreateInput{Port: port, Name: "billing", Project: "dashboard"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Preview.ID == first.Preview.ID || second.Preview.TargetPort != port {
		t.Fatalf("second preview did not share the port: %+v", second.Preview)
	}

	stopped, err := Stop(ctx, deps, StopInput{Ref: first.Preview.ID, UID: first.Preview.CreatedByUID})
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped.Stopped) != 1 || stopped.Stopped[0].ID != first.Preview.ID {
		t.Fatalf("%+v", stopped)
	}
	file, err := deps.Store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file.Find(second.Preview.ID); !ok {
		t.Fatal("stopping one preview removed the other")
	}
	if fake.count() != 1 {
		t.Fatalf("routes after stop %d", fake.count())
	}
}

func TestCreateRollsBackWhenPublicCheckFails(t *testing.T) {
	fake, srv := newFakeCaddy(t)
	deps := testDeps(t, srv.URL, true)
	port := listenLocal(t)
	_, err := Create(context.Background(), deps, CreateInput{Port: port, Name: "auth", Project: "app"})
	if err == nil || clierr.CodeOf(err) != "PUBLIC_UNREACHABLE" {
		t.Fatalf("err = %v", err)
	}
	file, readErr := deps.Store.Read()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(file.Previews) != 0 || fake.count() != 0 {
		t.Fatalf("rollback left state=%d routes=%d", len(file.Previews), fake.count())
	}
}

func TestCreateRequiresAListener(t *testing.T) {
	_, srv := newFakeCaddy(t)
	deps := testDeps(t, srv.URL, false)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	_, err = Create(context.Background(), deps, CreateInput{Port: port, Project: "app"})
	if clierr.CodeOf(err) != "TARGET_UNREACHABLE" {
		t.Fatalf("err = %v", err)
	}
}

func TestGCRemovesExpiredStaleAndOrphans(t *testing.T) {
	fake, srv := newFakeCaddy(t)
	deps := testDeps(t, srv.URL, false)
	ctx := context.Background()
	client := caddy.New(srv.URL)
	if err := client.UpsertRoute(ctx, "srv0", caddy.BuildRoute("preview-pv-orphan", "orphan.preview.example.com", "127.0.0.1:9")); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).UTC()
	longAgo := time.Now().Add(-2 * time.Hour).UTC()
	err := deps.Store.Update(func(file *state.File) error {
		file.Put(state.Preview{
			ID:           "pv_old1",
			Project:      "app",
			Hostname:     "app--old1.preview.example.com",
			TargetHost:   "127.0.0.1",
			TargetPort:   65530,
			CreatedAt:    longAgo,
			ExpiresAt:    &past,
			CaddyRouteID: "preview-pv-old1",
		})
		file.Put(state.Preview{
			ID:           "pv_dead",
			Project:      "app",
			Hostname:     "app--dead.preview.example.com",
			TargetHost:   "127.0.0.1",
			TargetPort:   65531,
			CreatedAt:    longAgo,
			CaddyRouteID: "preview-pv-dead",
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.UpsertRoute(ctx, "srv0", caddy.BuildRoute("preview-pv-old1", "app--old1.preview.example.com", "127.0.0.1:65530")); err != nil {
		t.Fatal(err)
	}

	result, err := GC(ctx, deps, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 3 || fake.count() != 2 {
		t.Fatalf("dry-run removed %#v routes %d", result.Removed, fake.count())
	}

	result, err = GC(ctx, deps, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Removed) != 3 {
		t.Fatalf("removed %#v", result.Removed)
	}
	file, err := deps.Store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(file.Previews) != 0 || fake.count() != 0 {
		t.Fatalf("state %d routes %d", len(file.Previews), fake.count())
	}
}
