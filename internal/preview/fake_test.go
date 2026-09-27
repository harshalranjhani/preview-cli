package preview

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
)

// caddyFake is a small stand-in for the local Caddy admin API.
type caddyFake struct {
	mu     sync.Mutex
	domain string
	routes map[string][]byte
}

func (f *caddyFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/config/":
		routes := []any{
			map[string]any{"match": []any{map[string]any{"host": []string{"*." + f.domain}}}},
		}
		for _, raw := range f.routes {
			var decoded any
			_ = json.Unmarshal(raw, &decoded)
			routes = append(routes, decoded)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"apps": map[string]any{
				"http": map[string]any{
					"servers": map[string]any{
						"srv0": map[string]any{"routes": routes},
					},
				},
			},
		})
	case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/routes/0"):
		body, _ := io.ReadAll(r.Body)
		var probe struct {
			ID string `json:"@id"`
		}
		if json.Unmarshal(body, &probe) != nil || probe.ID == "" {
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
