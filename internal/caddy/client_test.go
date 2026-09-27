package caddy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRouteLifecycle(t *testing.T) {
	fake := newFake("preview.example.com")
	srv := httptest.NewServer(fake)
	defer srv.Close()

	client := New(srv.URL)
	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	name, err := client.ServerName(ctx, "preview.example.com", "")
	if err != nil || name != "srv0" {
		t.Fatalf("server %s %v", name, err)
	}
	route := BuildRoute("preview-pv-k7p2", "app--fix--k7p2.preview.example.com", "127.0.0.1:3000")
	if err := client.UpsertRoute(ctx, name, route); err != nil {
		t.Fatal(err)
	}
	ok, err := client.RouteExists(ctx, route.ID)
	if err != nil || !ok {
		t.Fatalf("exists %v %v", ok, err)
	}
	ids, err := client.PreviewRouteIDs(ctx)
	if err != nil || len(ids) != 1 || ids[0] != route.ID {
		t.Fatalf("ids %v %v", ids, err)
	}
	if err := client.DeleteRoute(ctx, route.ID); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteRoute(ctx, route.ID); err != nil {
		t.Fatal("second delete should be idempotent")
	}
	ok, err = client.RouteExists(ctx, route.ID)
	if err != nil || ok {
		t.Fatalf("still exists %v %v", ok, err)
	}
}

type fakeCaddy struct {
	mu     sync.Mutex
	domain string
	routes map[string]json.RawMessage
}

func newFake(domain string) *fakeCaddy {
	return &fakeCaddy{domain: domain, routes: map[string]json.RawMessage{}}
}

func (f *fakeCaddy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		_ = json.NewEncoder(w).Encode(f.snapshot())
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/routes/0"):
		body, _ := io.ReadAll(r.Body)
		var probe struct {
			ID string `json:"@id"`
		}
		if err := json.Unmarshal(body, &probe); err != nil || probe.ID == "" {
			http.Error(w, "bad route", http.StatusBadRequest)
			return
		}
		f.routes[probe.ID] = body
		w.WriteHeader(http.StatusCreated)
	case strings.HasPrefix(r.URL.Path, "/id/"):
		id := strings.TrimPrefix(r.URL.Path, "/id/")
		if _, ok := f.routes[id]; !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodDelete {
			delete(f.routes, id)
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(f.routes[id])
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCaddy) snapshot() map[string]any {
	routes := []any{
		map[string]any{"match": []any{map[string]any{"host": []string{"*." + f.domain}}}},
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
