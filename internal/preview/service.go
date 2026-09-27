package preview

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/harshalranjhani/preview-cli/internal/caddy"
	"github.com/harshalranjhani/preview-cli/internal/clierr"
	"github.com/harshalranjhani/preview-cli/internal/config"
	"github.com/harshalranjhani/preview-cli/internal/exitcode"
	"github.com/harshalranjhani/preview-cli/internal/health"
	"github.com/harshalranjhani/preview-cli/internal/hostname"
	"github.com/harshalranjhani/preview-cli/internal/state"
	"github.com/harshalranjhani/preview-cli/internal/ui"
)

// Deps is everything a preview operation needs from the local machine.
type Deps struct {
	Config  config.Config
	Store   *state.Store
	Caddy   *caddy.Client
	Project config.ProjectFile
	CWD     string
	Debug   func(string, ...any)
}

func (d Deps) debugf(format string, args ...any) {
	if d.Debug != nil {
		d.Debug(format, args...)
	}
}

// CreateInput is a request to publish one local port.
type CreateInput struct {
	Port          int
	Name          string
	Project       string
	TTL           string
	SkipLocal     bool
	SkipPublic    bool
	KeepOnFailure bool
}

// CreateResult is a published preview.
type CreateResult struct {
	Preview  state.Preview
	Warnings []string
	View     map[string]any
}

// Create publishes a temporary HTTPS route to a local port.
func Create(ctx context.Context, d Deps, in CreateInput) (CreateResult, error) {
	cfg := d.Config
	if err := cfg.CheckPort(in.Port); err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}
	if err := config.ValidateTarget(cfg.Routing.TargetHost, cfg.AllowNonLoopback()); err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}

	project, gitRoot, err := config.ResolveProjectName(in.Project, d.Project, d.CWD)
	if err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}
	name, err := config.ResolveName(in.Name, d.Project.DefaultNamePrefix)
	if err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}
	ttl, err := config.ResolveTTL(in.TTL, d.Project.DefaultTTL, cfg)
	if err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}
	d.debugf("project=%s name=%s ttl=%s target=%s:%d", project, name, ttl, cfg.Routing.TargetHost, in.Port)

	if !in.SkipLocal {
		d.debugf("probing %s:%d", cfg.Routing.TargetHost, in.Port)
		if err := health.TCP(ctx, cfg.Routing.TargetHost, in.Port, 2*time.Second); err != nil {
			return CreateResult{}, clierr.TargetUnreachable(cfg.Routing.TargetHost, in.Port)
		}
	}

	if err := d.Caddy.Ping(ctx); err != nil {
		return CreateResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	serverName, err := d.Caddy.ServerName(ctx, cfg.Server.BaseDomain, cfg.Caddy.ServerName)
	if err != nil {
		return CreateResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	d.debugf("caddy server=%s", serverName)

	now := time.Now().UTC().Truncate(time.Second)
	dur, never, err := config.ParseTTL(ttl)
	if err != nil {
		return CreateResult{}, clierr.Usage("%s", err.Error())
	}
	var expires *time.Time
	if !never {
		t := now.Add(dur)
		expires = &t
	}

	var created state.Preview
	err = d.Store.Update(func(file *state.File) error {
		var short string
		var host string
		var id string
		for attempt := 0; attempt < 8; attempt++ {
			var genErr error
			short, genErr = hostname.ShortID()
			if genErr != nil {
				return genErr
			}
			id = hostname.PreviewID(short)
			host, genErr = hostname.Build(cfg.Server.HostnameTemplate, project, name, short, cfg.Server.BaseDomain)
			if genErr != nil {
				return clierr.Usage("%s", genErr.Error())
			}
			if !file.IDTaken(id) && !file.HostnameTaken(host) {
				break
			}
			if attempt == 7 {
				return clierr.New(exitcode.Conflict, "HOSTNAME_CONFLICT", "could not allocate a unique preview hostname")
			}
		}
		d.debugf("hostname=%s id=%s", host, id)
		preview := state.Preview{
			ID:           id,
			Project:      project,
			Name:         name,
			Hostname:     host,
			TargetHost:   cfg.Routing.TargetHost,
			TargetPort:   in.Port,
			CreatedAt:    now,
			ExpiresAt:    expires,
			CreatedByUID: os.Getuid(),
			CWD:          d.CWD,
			GitRoot:      gitRoot,
			CaddyRouteID: hostname.RouteID(id),
		}
		route := caddy.BuildRoute(preview.CaddyRouteID, preview.Hostname, preview.Dial())
		if err := d.Caddy.UpsertRoute(ctx, serverName, route); err != nil {
			return clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		file.Put(preview)
		created = preview
		return nil
	})
	if err != nil {
		if created.CaddyRouteID != "" {
			_ = d.Caddy.DeleteRoute(ctx, created.CaddyRouteID)
		}
		return CreateResult{}, err
	}

	var warnings []string
	verify := cfg.VerifyPublic() && !in.SkipPublic
	if verify {
		d.debugf("checking %s", created.PublicURL())
		if _, err := health.Public(ctx, created.PublicURL(), cfg.HealthTimeout()); err != nil {
			d.debugf("public check failed: %v", err)
			if _, loopErr := health.LoopbackHTTPS(ctx, created.Hostname, cfg.HealthTimeout()); loopErr != nil {
				d.debugf("loopback check failed: %v", loopErr)
				if !in.KeepOnFailure {
					_ = d.Caddy.DeleteRoute(ctx, created.CaddyRouteID)
					_ = d.Store.Update(func(file *state.File) error {
						file.Remove(created.ID)
						return nil
					})
					return CreateResult{}, clierr.New(exitcode.Generic, "PUBLIC_UNREACHABLE", fmt.Sprintf("preview route did not respond at %s: %v", created.PublicURL(), err))
				}
				warnings = append(warnings, fmt.Sprintf("public check failed and the route was kept: %v", err))
			} else {
				warnings = append(warnings, "the route works on this server, but the public URL could not be reached from here; check DNS and that the server can reach its own public IP")
			}
		}
	}

	return CreateResult{Preview: created, Warnings: warnings, View: viewPreview(created, dur, never)}, nil
}

func viewPreview(p state.Preview, ttl time.Duration, never bool) map[string]any {
	view := map[string]any{
		"id":         p.ID,
		"project":    p.Project,
		"name":       p.Name,
		"hostname":   p.Hostname,
		"url":        p.PublicURL(),
		"target":     p.TargetURL(),
		"port":       p.TargetPort,
		"created_at": p.CreatedAt.Format(time.RFC3339),
		"expires_at": nil,
		"expires_in": "never",
	}
	if p.Name == "" {
		delete(view, "name")
	}
	if p.ExpiresAt != nil {
		view["expires_at"] = p.ExpiresAt.Format(time.RFC3339)
		view["expires_in"] = ui.FormatDuration(ttl)
	}
	if never {
		view["expires_in"] = "never"
	}
	return view
}

// StopInput selects previews to unpublish.
type StopInput struct {
	Ref            string
	Project        string
	CurrentProject bool
	All            bool
	UID            int
}

// StopResult lists previews that are no longer routed.
type StopResult struct {
	Stopped  []state.Preview
	Warnings []string
}

// Stop removes routes. It does not stop the application process.
func Stop(ctx context.Context, d Deps, in StopInput) (StopResult, error) {
	if err := d.Caddy.Ping(ctx); err != nil {
		return StopResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	file, err := d.Store.Read()
	if err != nil {
		return StopResult{}, err
	}

	var selected []state.Preview
	switch {
	case in.All:
		for _, p := range file.Previews {
			if canManage(p, in.UID) {
				selected = append(selected, p)
			}
		}
	case in.CurrentProject || in.Project != "":
		project, _, err := config.ResolveProjectName(in.Project, d.Project, d.CWD)
		if err != nil {
			return StopResult{}, clierr.Usage("%s", err.Error())
		}
		for _, p := range file.Previews {
			if p.Project == project && canManage(p, in.UID) {
				selected = append(selected, p)
			}
		}
	default:
		p, err := file.Resolve(in.Ref)
		if err != nil {
			return StopResult{}, clierr.New(exitcode.Generic, "NOT_FOUND", err.Error())
		}
		if !canManage(p, in.UID) {
			return StopResult{}, clierr.New(exitcode.Usage, "FORBIDDEN", fmt.Sprintf("preview %s is owned by uid %d", p.ID, p.CreatedByUID))
		}
		selected = []state.Preview{p}
	}

	if len(selected) == 0 {
		return StopResult{}, nil
	}

	var warnings []string
	for _, p := range selected {
		exists, err := d.Caddy.RouteExists(ctx, p.CaddyRouteID)
		if err != nil {
			return StopResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		if exists {
			if err := d.Caddy.DeleteRoute(ctx, p.CaddyRouteID); err != nil {
				return StopResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("caddy route for %s was already gone", p.ID))
		}
		gone, err := d.Caddy.RouteExists(ctx, p.CaddyRouteID)
		if err != nil {
			return StopResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		if gone {
			return StopResult{}, clierr.New(exitcode.Caddy, "CADDY_UNAVAILABLE", fmt.Sprintf("caddy still has route %s", p.CaddyRouteID))
		}
	}

	err = d.Store.Update(func(file *state.File) error {
		for _, p := range selected {
			file.Remove(p.ID)
		}
		return nil
	})
	if err != nil {
		return StopResult{}, err
	}
	return StopResult{Stopped: selected, Warnings: warnings}, nil
}

func canManage(p state.Preview, uid int) bool {
	return uid == 0 || p.CreatedByUID == uid
}

// List returns previews, optionally limited to one project.
func List(d Deps, projectFilter string) ([]state.Preview, error) {
	file, err := d.Store.Read()
	if err != nil {
		return nil, err
	}
	if projectFilter == "" {
		if file.Previews == nil {
			return []state.Preview{}, nil
		}
		return file.Previews, nil
	}
	project, _, err := config.ResolveProjectName(projectFilter, d.Project, d.CWD)
	if err != nil {
		return nil, clierr.Usage("%s", err.Error())
	}
	out := make([]state.Preview, 0)
	for _, p := range file.Previews {
		if p.Project == project {
			out = append(out, p)
		}
	}
	return out, nil
}

// Status is a preview plus live checks.
type Status struct {
	state.Preview
	URL            string `json:"url"`
	RoutePresent   bool   `json:"route_present"`
	LocalReachable bool   `json:"local_reachable"`
	PublicStatus   int    `json:"public_status,omitempty"`
	PublicError    string `json:"public_error,omitempty"`
	ExpiresIn      string `json:"expires_in"`
}

// Inspect loads one preview and checks its route and target.
func Inspect(ctx context.Context, d Deps, ref string) (Status, error) {
	file, err := d.Store.Read()
	if err != nil {
		return Status{}, err
	}
	p, err := file.Resolve(ref)
	if err != nil {
		return Status{}, clierr.New(exitcode.Generic, "NOT_FOUND", err.Error())
	}
	st := Status{
		Preview:   p,
		URL:       p.PublicURL(),
		ExpiresIn: "never",
	}
	st.ExpiresIn = ui.FormatRemaining(p.ExpiresAt, time.Now().UTC())
	if err := d.Caddy.Ping(ctx); err != nil {
		st.PublicError = err.Error()
		return st, nil
	}
	exists, err := d.Caddy.RouteExists(ctx, p.CaddyRouteID)
	if err != nil {
		st.PublicError = err.Error()
		return st, nil
	}
	st.RoutePresent = exists
	if err := health.TCP(ctx, p.TargetHost, p.TargetPort, 2*time.Second); err == nil {
		st.LocalReachable = true
	}
	res, err := health.Public(ctx, p.PublicURL(), d.Config.HealthTimeout())
	if err != nil {
		if loop, loopErr := health.LoopbackHTTPS(ctx, p.Hostname, d.Config.HealthTimeout()); loopErr == nil {
			st.PublicStatus = loop.Status
			st.PublicError = "public DNS check failed; local HTTPS route responded"
			return st, nil
		}
		st.PublicError = err.Error()
		return st, nil
	}
	st.PublicStatus = res.Status
	return st, nil
}

// GCItem is one preview or orphan route selected for cleanup.
type GCItem struct {
	ID       string `json:"id,omitempty"`
	RouteID  string `json:"route_id,omitempty"`
	Hostname string `json:"hostname,omitempty"`
	Reason   string `json:"reason"`
}

// GCResult is the cleanup report.
type GCResult struct {
	Removed []GCItem `json:"removed"`
	DryRun  bool     `json:"dry_run"`
}

// GC removes expired previews, stale targets, and orphaned Caddy routes.
func GC(ctx context.Context, d Deps, dryRun bool) (GCResult, error) {
	if err := d.Caddy.Ping(ctx); err != nil {
		return GCResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	file, err := d.Store.Read()
	if err != nil {
		return GCResult{}, err
	}
	now := time.Now().UTC()
	var doomed []state.Preview
	var items []GCItem
	for _, p := range file.Previews {
		switch {
		case p.Expired(now):
			doomed = append(doomed, p)
			items = append(items, GCItem{ID: p.ID, RouteID: p.CaddyRouteID, Hostname: p.Hostname, Reason: "expired"})
		case targetStale(ctx, p, now, d.Config.StaleAfter()):
			doomed = append(doomed, p)
			items = append(items, GCItem{ID: p.ID, RouteID: p.CaddyRouteID, Hostname: p.Hostname, Reason: "stale"})
		}
	}

	if dryRun {
		routeIDs, err := d.Caddy.PreviewRouteIDs(ctx)
		if err != nil {
			return GCResult{}, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		for _, id := range routeIDs {
			if !containsRoute(file.Previews, id) {
				items = append(items, GCItem{RouteID: id, Reason: "orphan"})
			}
		}
		if items == nil {
			items = []GCItem{}
		}
		return GCResult{Removed: items, DryRun: true}, nil
	}

	err = d.Store.Update(func(file *state.File) error {
		for _, p := range doomed {
			if err := d.Caddy.DeleteRoute(ctx, p.CaddyRouteID); err != nil {
				return clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
			}
			file.Remove(p.ID)
		}
		routeIDs, err := d.Caddy.PreviewRouteIDs(ctx)
		if err != nil {
			return clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		for _, id := range routeIDs {
			if containsRoute(file.Previews, id) {
				continue
			}
			if err := d.Caddy.DeleteRoute(ctx, id); err != nil {
				return clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
			}
			items = append(items, GCItem{RouteID: id, Reason: "orphan"})
		}
		return nil
	})
	if err != nil {
		return GCResult{}, err
	}
	if items == nil {
		items = []GCItem{}
	}
	return GCResult{Removed: items, DryRun: false}, nil
}

func targetStale(ctx context.Context, p state.Preview, now time.Time, staleAfter time.Duration) bool {
	if now.Sub(p.CreatedAt) < staleAfter {
		return false
	}
	err := health.TCP(ctx, p.TargetHost, p.TargetPort, 2*time.Second)
	return err != nil
}

func containsRoute(previews []state.Preview, routeID string) bool {
	for _, p := range previews {
		if p.CaddyRouteID == routeID {
			return true
		}
	}
	return false
}

// Sync restores every unexpired preview route into Caddy.
func Sync(ctx context.Context, d Deps) (int, error) {
	if err := d.Caddy.Ping(ctx); err != nil {
		return 0, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	file, err := d.Store.Read()
	if err != nil {
		return 0, err
	}
	if len(file.Previews) == 0 {
		return 0, nil
	}
	serverName, err := d.Caddy.ServerName(ctx, d.Config.Server.BaseDomain, d.Config.Caddy.ServerName)
	if err != nil {
		return 0, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
	}
	now := time.Now().UTC()
	n := 0
	for _, p := range file.Previews {
		if p.Expired(now) {
			continue
		}
		route := caddy.BuildRoute(p.CaddyRouteID, p.Hostname, p.Dial())
		if err := d.Caddy.UpsertRoute(ctx, serverName, route); err != nil {
			return n, clierr.Wrap(exitcode.Caddy, "CADDY_UNAVAILABLE", err)
		}
		n++
	}
	return n, nil
}
