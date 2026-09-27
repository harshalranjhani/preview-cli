package caddy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrSiteNotFound means the loaded Caddy config has no wildcard site for the base domain.
var ErrSiteNotFound = errors.New("preview site not found")

const routePrefix = "preview-"

// Client talks to the local Caddy admin API.
type Client struct {
	AdminURL string
	HTTP     *http.Client
}

func New(adminURL string) *Client {
	return &Client{
		AdminURL: strings.TrimRight(adminURL, "/"),
		HTTP: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

// Route is a host-matched reverse proxy installed through the admin API.
type Route struct {
	ID       string  `json:"@id"`
	Match    []Match `json:"match"`
	Handle   []any   `json:"handle"`
	Terminal bool    `json:"terminal"`
}

// Match is a Caddy request matcher.
type Match struct {
	Host []string `json:"host"`
}

type reverseProxy struct {
	Handler       string     `json:"handler"`
	Upstreams     []upstream `json:"upstreams"`
	FlushInterval int        `json:"flush_interval"`
}

type upstream struct {
	Dial string `json:"dial"`
}

// BuildRoute constructs the route for one preview.
func BuildRoute(routeID, hostname, dial string) Route {
	return Route{
		ID:    routeID,
		Match: []Match{{Host: []string{hostname}}},
		Handle: []any{
			reverseProxy{
				Handler:       "reverse_proxy",
				Upstreams:     []upstream{{Dial: dial}},
				FlushInterval: -1,
			},
		},
		Terminal: true,
	}
}

// WaitUntilReady polls the admin API until it responds or the deadline passes.
func (c *Client) WaitUntilReady(ctx context.Context) error {
	deadline := time.Now().Add(5 * time.Second)
	var last error
	for {
		if err := c.Ping(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			if last == nil {
				last = ctx.Err()
			}
			return last
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Ping checks that the admin API responds.
func (c *Client) Ping(ctx context.Context) error {
	resp, body, err := c.do(ctx, http.MethodGet, "/config/", nil)
	if err != nil {
		return fmt.Errorf("caddy admin API at %s is not reachable: %w", c.AdminURL, err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("caddy admin API at %s returned %s: %s", c.AdminURL, resp.Status, truncate(body))
	}
	return nil
}

// ServerName finds the HTTP server that owns the wildcard site.
// preferred is used when it exists in the config.
func (c *Client) ServerName(ctx context.Context, baseDomain, preferred string) (string, error) {
	raw, err := c.config(ctx)
	if err != nil {
		return "", err
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("parse caddy config: %w", err)
	}
	servers, err := httpServers(root)
	if err != nil {
		return "", err
	}
	want := "*." + baseDomain
	for name, srv := range servers {
		for _, host := range hostMatchers(srv) {
			if host == want || host == baseDomain {
				return name, nil
			}
		}
	}
	if preferred != "" {
		if _, ok := servers[preferred]; ok {
			return preferred, nil
		}
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	return "", fmt.Errorf("%w: no site for *.%s (servers: %s); run: sudo preview server apply", ErrSiteNotFound, baseDomain, strings.Join(names, ", "))
}

// UpsertRoute inserts a preview route at the front of the server so it wins over the wildcard site.
func (c *Client) UpsertRoute(ctx context.Context, server string, route Route) error {
	_ = c.DeleteRoute(ctx, route.ID)
	payload, err := json.Marshal(route)
	if err != nil {
		return err
	}
	// PUT inserts at index 0. POST appends, and the wildcard site is terminal,
	// so an appended route would never run for these hostnames.
	path := "/config/apps/http/servers/" + url.PathEscape(server) + "/routes/0"
	resp, body, err := c.do(ctx, http.MethodPut, path, payload)
	if err != nil {
		return fmt.Errorf("install caddy route: %w", err)
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("install caddy route %s: %s: %s", route.ID, resp.Status, truncate(body))
	}
	return nil
}

// DeleteRoute removes one preview route. A missing route is success.
func (c *Client) DeleteRoute(ctx context.Context, id string) error {
	resp, body, err := c.do(ctx, http.MethodDelete, "/id/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("delete caddy route %s: %w", id, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("delete caddy route %s: %s: %s", id, resp.Status, truncate(body))
	}
	return nil
}

// RouteExists reports whether a route id is currently loaded.
func (c *Client) RouteExists(ctx context.Context, id string) (bool, error) {
	resp, body, err := c.do(ctx, http.MethodGet, "/id/"+url.PathEscape(id), nil)
	if err != nil {
		return false, fmt.Errorf("read caddy route %s: %w", id, err)
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("read caddy route %s: %s: %s", id, resp.Status, truncate(body))
	}
	return true, nil
}

// PreviewRouteIDs lists route ids owned by preview.
func (c *Client) PreviewRouteIDs(ctx context.Context) ([]string, error) {
	raw, err := c.config(ctx)
	if err != nil {
		return nil, err
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse caddy config: %w", err)
	}
	var ids []string
	collectIDs(root, routePrefix, &ids)
	return ids, nil
}

// HasDomain reports whether the loaded config mentions the wildcard site.
func (c *Client) HasDomain(ctx context.Context, baseDomain string) (bool, error) {
	_, err := c.ServerName(ctx, baseDomain, "")
	if errors.Is(err, ErrSiteNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *Client) config(ctx context.Context) ([]byte, error) {
	resp, body, err := c.do(ctx, http.MethodGet, "/config/", nil)
	if err != nil {
		return nil, fmt.Errorf("read caddy config: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("read caddy config: %s: %s", resp.Status, truncate(body))
	}
	return body, nil
}

func (c *Client) do(ctx context.Context, method, path string, payload []byte) (*http.Response, []byte, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.AdminURL+path, body)
	if err != nil {
		return nil, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "preview")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp, nil, err
	}
	return resp, data, nil
}

func httpServers(root any) (map[string]any, error) {
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("caddy config is empty")
	}
	apps, _ := obj["apps"].(map[string]any)
	httpApp, _ := apps["http"].(map[string]any)
	servers, _ := httpApp["servers"].(map[string]any)
	if len(servers) == 0 {
		return nil, fmt.Errorf("%w: caddy has no HTTP servers; run: sudo preview server apply", ErrSiteNotFound)
	}
	return servers, nil
}

func hostMatchers(v any) []string {
	var hosts []string
	var walk func(any)
	walk = func(node any) {
		switch t := node.(type) {
		case map[string]any:
			if raw, ok := t["host"]; ok {
				if arr, ok := raw.([]any); ok {
					for _, item := range arr {
						if s, ok := item.(string); ok {
							hosts = append(hosts, s)
						}
					}
				}
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	return hosts
}

func collectIDs(v any, prefix string, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if id, ok := t["@id"].(string); ok && strings.HasPrefix(id, prefix) {
			*out = append(*out, id)
		}
		for _, child := range t {
			collectIDs(child, prefix, out)
		}
	case []any:
		for _, child := range t {
			collectIDs(child, prefix, out)
		}
	}
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		return s[:400] + "..."
	}
	if s == "" {
		return "no response body"
	}
	return s
}
