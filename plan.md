# Preview CLI — Detailed Implementation Plan

## 1. Project Overview

`preview` is a small, self-hosted Go CLI that behaves like a private, domain-aware version of ngrok for applications already running on a server.

Its job is intentionally narrow:

> Given a local HTTP port, create a temporary HTTPS URL on a configured custom domain, route that URL to the port through Caddy, and remove the route when the preview is stopped or expires.

The CLI does **not** start applications, detect frameworks, run tests, commit code, or push Git changes. Those responsibilities stay with the developer or coding agent. This keeps `preview` reusable across T3/Next.js, Vite, Django, Flask, Go, Rails, Dockerized apps, or anything else that exposes an HTTP port.

Typical usage:

```bash
# Agent/developer starts the app normally.
pnpm dev

# App is listening on port 3000.
preview http 3000 --name auth-fix
```

Example output:

```text
Preview created

Project:    my-app
Target:     http://127.0.0.1:3000
URL:        https://my-app--auth-fix--k7p2.preview.example.com
Expires:    2h
ID:         pv_k7p2
```

When testing is complete:

```bash
preview stop pv_k7p2
```

The URL immediately stops routing to the application.

---

## 2. Core Goals

The project should be:

- **Simple** — one static Go binary with minimal dependencies.
- **Port-based** — the CLI only needs to know the application's listening port.
- **Framework-agnostic** — no Next.js/Vite/Django/etc. logic.
- **Self-hosted** — all routing and TLS stay on infrastructure controlled by the user.
- **Custom-domain friendly** — usable with any domain and any server.
- **Agent-friendly** — predictable commands and machine-readable output.
- **Ephemeral** — previews can expire automatically and should be easy to clean up.
- **Safe** — only local targets are exposed by default, Caddy administration remains private, and stale routes are cleaned up.
- **Portable** — repository can be published publicly and installed on arbitrary Linux servers.
- **Low-maintenance** — Caddy manages HTTPS certificates automatically.

---

## 3. Non-Goals

The first version should explicitly avoid becoming a deployment platform.

`preview` will not:

- start or stop application development servers;
- guess project start commands;
- install project dependencies;
- manage Docker containers;
- build applications;
- run tests or linters;
- commit, merge, or push Git changes;
- create Git branches or pull requests;
- replace CI/CD;
- expose arbitrary remote machines through a relay;
- provide Cloudflare Tunnel-like NAT traversal.

Those can be layered on later, but the base tool remains a temporary reverse-proxy manager.

---

## 4. Intended Agent Workflow

A coding agent can have a global skill/instruction similar to:

```text
After completing an implementation:

1. Start the application using the repository's normal development command.
2. Determine the local HTTP port on which it is listening.
3. Run:
      preview http <port> --name <short-task-name>
4. Wait for the command to confirm that the public URL is healthy.
5. Give the preview URL to the user.
6. Do not commit or push until the user approves.
7. After approval:
      preview stop <preview-id>
   then stop the application process, run the repository's required checks,
   commit the changes, and push them.
8. If the user rejects the changes, stop the preview and continue editing.
```

The agent therefore only needs to understand the repository well enough to start the app. `preview` handles networking.

---

## 5. High-Level Architecture

```text
                        Internet
                           │
                           │ HTTPS
                           ▼
                temporary preview hostname
                           │
                           ▼
                  Cloudflare DNS only
                    (grey cloud)
                           │
                           ▼
                    Public VPS :443
                           │
                           ▼
                         Caddy
                     TLS + routing
                           │
               ┌───────────┼───────────┐
               ▼           ▼           ▼
          127.0.0.1:3000  :5173      :8000
               │           │           │
             App A       App B       App C
```

The CLI communicates only with local infrastructure:

```text
preview CLI
    │
    ├── local state store
    │
    └── Caddy Admin API on localhost
```

No external SaaS is required.

---

## 6. DNS and Domain Design

### 6.1 Recommended DNS setup

For a reusable server-wide installation:

```text
*.preview.example.com  A  <VPS_PUBLIC_IP>
```

The Cloudflare record should be **DNS only**, not proxied.

This means:

```text
Browser ──HTTPS──> Caddy on VPS
```

Cloudflare is only authoritative DNS for the hostname and does not terminate browser TLS.

### 6.2 Why DNS-only matters

Cloudflare Universal SSL on the free plan is not suitable for arbitrary deeper preview hostnames when Cloudflare is terminating HTTPS.

By using DNS-only records:

- Cloudflare does not need an edge certificate for the preview hostname.
- Caddy owns TLS.
- Caddy can obtain a wildcard certificate through ACME DNS-01.
- The solution remains independent of Cloudflare's proxy certificate limitations.

### 6.3 Project-aware preview URLs

Preview URLs should include the project name.

The recommended default format is:

```text
<project>--<preview-name>--<short-id>.preview.example.com
```

Examples:

```text
dashboard--auth-fix--k7p2.preview.example.com
api--rate-limit--4m9x.preview.example.com
storefront--checkout--p3qd.preview.example.com
```

This deliberately keeps the whole generated identifier inside the **single wildcard label** covered by:

```text
*.preview.example.com
```

That lets one wildcard certificate cover every project on the preview server.

### 6.4 Optional project-specific wildcard mode

The CLI should also support deployments where the configured wildcard already contains the project name.

Example configured wildcard:

```text
*.my-app.preview.example.com
```

Generated URLs can then be:

```text
auth-fix--k7p2.my-app.preview.example.com
checkout--p3qd.my-app.preview.example.com
```

This is useful if users want separate preview namespaces per project.

Configuration should therefore support a hostname template rather than hard-code one URL structure.

Example:

```yaml
hostname_template: "{{project}}--{{name}}--{{id}}.preview.example.com"
```

or:

```yaml
hostname_template: "{{name}}--{{id}}.{{project}}.preview.example.com"
```

The default should favor the first form because it allows a single server-wide wildcard certificate.

### 6.5 Project name resolution

The CLI resolves `project` in this order:

1. `--project <name>`
2. `.preview.yaml` in the repository
3. Git repository root directory name
4. Current directory name

Names are normalized into DNS-safe slugs:

```text
My Cool_App → my-cool-app
```

Rules:

- lowercase only;
- `a-z`, `0-9`, `-`;
- collapse duplicate dashes;
- strip leading/trailing dashes;
- enforce DNS label length limits;
- reserve enough room for preview name and unique ID.

---

## 7. TLS / HTTPS

Caddy owns HTTPS completely.

Certbot is not required.

For wildcard certificates, Caddy uses ACME DNS-01 through the DNS provider configured by the server operator.

For Cloudflare, the Caddy build needs the Cloudflare DNS provider module.

Conceptual configuration:

```caddy
*.preview.example.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    # dynamic preview routes are installed through the Admin API
}
```

The Cloudflare API token should have only the minimum DNS permissions required for the relevant zone.

### TLS responsibilities

Caddy handles:

- certificate issuance;
- wildcard DNS challenge;
- certificate storage;
- certificate renewal;
- HTTPS listener;
- SNI;
- TLS termination.

`preview` handles:

- hostname generation;
- hostname → local-port routing;
- route creation/removal;
- preview state;
- expiration.

---

## 8. Caddy Integration

### 8.1 Preferred approach: Caddy Admin API

The first version should use Caddy's local Admin API rather than modifying Caddyfiles and reloading the entire service.

Advantages:

- fast route creation;
- fast route deletion;
- no Caddy restart;
- no temporary config files;
- cleaner concurrency;
- lower chance of disturbing unrelated sites.

The Admin API should remain bound to localhost only.

Example:

```text
127.0.0.1:2019
```

It must never be exposed publicly.

### 8.2 Route behavior

For a preview:

```text
dashboard--auth-fix--k7p2.preview.example.com
```

the CLI creates a route equivalent to:

```text
Host == dashboard--auth-fix--k7p2.preview.example.com
    ↓
reverse_proxy 127.0.0.1:3000
```

Every inserted route should have a stable preview identifier so it can be updated or removed safely.

### 8.3 Concurrency

Multiple agents may create previews simultaneously.

The CLI must prevent one process from overwriting another process's Caddy changes.

Use:

- atomic Caddy API operations where possible;
- a process/file lock around local state modifications;
- unique preview IDs;
- route-specific deletion instead of replacing the whole Caddy config.

---

## 9. CLI Interface

Binary name:

```text
preview
```

### 9.1 `preview http`

Create a temporary HTTP preview.

```bash
preview http 3000
```

With a friendly task name:

```bash
preview http 3000 --name auth-fix
```

Explicit project:

```bash
preview http 3000 --project dashboard --name auth-fix
```

Custom TTL:

```bash
preview http 3000 --ttl 30m
```

No expiry:

```bash
preview http 3000 --ttl 0
```

Machine-readable mode:

```bash
preview http 3000 --name auth-fix --json
```

Example JSON:

```json
{
  "id": "pv_k7p2",
  "project": "dashboard",
  "name": "auth-fix",
  "hostname": "dashboard--auth-fix--k7p2.preview.example.com",
  "url": "https://dashboard--auth-fix--k7p2.preview.example.com",
  "target": "http://127.0.0.1:3000",
  "port": 3000,
  "created_at": "2026-09-27T10:00:00Z",
  "expires_at": "2026-09-27T12:00:00Z"
}
```

### 9.2 `preview stop`

Stop by ID:

```bash
preview stop pv_k7p2
```

Stop by hostname:

```bash
preview stop dashboard--auth-fix--k7p2.preview.example.com
```

Stop all previews belonging to the current project:

```bash
preview stop --project
```

Stop everything owned by the current user:

```bash
preview stop --all
```

Potentially dangerous broad cleanup should require an explicit flag.

### 9.3 `preview list`

```bash
preview list
```

Example:

```text
ID        PROJECT      NAME       TARGET            URL                                      EXPIRES
pv_k7p2   dashboard    auth-fix   127.0.0.1:3000   https://dashboard--auth-fix--k7p2...     1h 42m
pv_4m9x   api          limits     127.0.0.1:8000   https://api--limits--4m9x...              24m
```

Filters:

```bash
preview list --project dashboard
preview list --json
```

### 9.4 `preview status`

```bash
preview status pv_k7p2
```

Displays:

- route state;
- target;
- local target health;
- public URL health;
- creation time;
- expiry;
- owning project;
- owning user/process metadata where available.

### 9.5 `preview init`

Interactive/simple bootstrap for project-local configuration:

```bash
preview init
```

Creates:

```text
.preview.yaml
```

Example:

```yaml
project: dashboard
```

The project file should be intentionally tiny.

Optional fields can override server defaults:

```yaml
project: dashboard
default_name_prefix: agent
default_ttl: 2h
```

Project configuration should never need Caddy credentials or Cloudflare credentials.

### 9.6 `preview doctor`

Validate installation:

```bash
preview doctor
```

Checks:

- global config exists;
- state directory writable;
- Caddy Admin API reachable;
- wildcard DNS resolves to this server;
- Caddy has a matching TLS configuration;
- configured target bind host is valid;
- certificate/DNS integration appears healthy;
- hostname template produces valid hostnames.

Output should clearly distinguish warnings from errors.

### 9.7 `preview gc`

Remove expired/stale previews:

```bash
preview gc
```

It should:

- find expired previews;
- detect routes whose target no longer accepts connections;
- remove stale Caddy routes;
- remove stale state entries.

Support:

```bash
preview gc --dry-run
```

### 9.8 `preview server`

Manage the server-side integration owned by the CLI.

```bash
sudo preview server init
sudo preview server apply
preview server render
preview server apply --dry-run
```

`server init` performs first-time setup. `server apply` regenerates and safely applies the built-in Caddy configuration after user configuration changes. `server render` allows inspection without mutation.

### 9.9 Other utility commands

```bash
preview version
preview completion bash
preview completion zsh
preview completion fish
```

---

## 10. Configuration Design

Configuration has two levels.

### 10.1 Server/global configuration

Default path:

```text
/etc/preview/config.yaml
```

The user configures **values**, not Caddy syntax. The Caddy configuration structure used by `preview` is embedded in the CLI and rendered automatically by `preview server init`.

Example user-facing configuration:

```yaml
server:
  base_domain: preview.example.com
  hostname_template: "{{project}}--{{name}}--{{id}}.preview.example.com"

dns:
  provider: cloudflare
  credentials_env: CLOUDFLARE_API_TOKEN

caddy:
  admin_url: http://127.0.0.1:2019
  managed_config_path: /etc/caddy/preview.caddy
  server_name: srv0

routing:
  target_host: 127.0.0.1
  allowed_port_min: 1024
  allowed_port_max: 65535

preview:
  default_ttl: 2h
  health_timeout: 15s
  verify_public_url: true

state:
  path: /var/lib/preview/state.json

security:
  allow_non_loopback_targets: false
```

Users should normally only need to change values such as:

- preview/base domain;
- hostname template;
- DNS provider and credential environment-variable name;
- default TTL;
- allowed port range;
- optional security settings;
- Caddy Admin API location when using a non-default installation.

They should **not** need to write reverse-proxy routes, TLS blocks, ACME configuration, route IDs, or Caddy JSON manually.

### 10.2 Project configuration

At Git root:

```text
.preview.yaml
```

Minimal:

```yaml
project: dashboard
```

Possible extended form:

```yaml
project: dashboard
default_ttl: 1h
```

### 10.3 Environment variable overrides

Useful for CI/automation:

```text
PREVIEW_CONFIG
PREVIEW_CADDY_ADMIN_URL
PREVIEW_BASE_DOMAIN
PREVIEW_PROJECT
PREVIEW_DEFAULT_TTL
```

CLI flags override environment variables; environment variables override files.

Recommended precedence:

```text
CLI flags
  ↓
environment
  ↓
project .preview.yaml
  ↓
global /etc/preview/config.yaml
  ↓
built-in defaults
```

---

## 11. State Management

The MVP can use a JSON state file protected by a file lock.

Example internal entry:

```json
{
  "id": "pv_k7p2",
  "project": "dashboard",
  "name": "auth-fix",
  "hostname": "dashboard--auth-fix--k7p2.preview.example.com",
  "target_host": "127.0.0.1",
  "target_port": 3000,
  "created_at": "2026-09-27T10:00:00Z",
  "expires_at": "2026-09-27T12:00:00Z",
  "created_by_uid": 1000,
  "cwd": "/srv/projects/dashboard",
  "git_root": "/srv/projects/dashboard",
  "caddy_route_id": "preview-pv-k7p2"
}
```

If concurrency or state complexity grows later, move to SQLite without changing the CLI UX.

State writes must use:

1. lock;
2. read latest state;
3. modify;
4. write to temporary file;
5. atomic rename;
6. unlock.

---

## 12. Preview Creation Lifecycle

`preview http 3000 --name auth-fix`

### Step 1 — Validate input

Validate:

- port is numeric;
- port is within configured allowed range;
- target host is allowed;
- name/project produce valid DNS-safe values.

### Step 2 — Verify local application

Probe:

```text
http://127.0.0.1:3000
```

A TCP connection is the minimum requirement.

Optionally perform an HTTP request.

If nothing is listening, fail immediately:

```text
Error: nothing appears to be listening on 127.0.0.1:3000
```

Support an override for unusual services:

```bash
preview http 3000 --skip-local-check
```

### Step 3 — Resolve project

Resolve from:

- CLI;
- `.preview.yaml`;
- Git root;
- working directory.

### Step 4 — Generate identity

Create:

```text
ID: pv_k7p2
name: auth-fix
```

The random ID prevents collisions and makes repeated previews safe.

### Step 5 — Generate hostname

Example:

```text
dashboard--auth-fix--k7p2.preview.example.com
```

### Step 6 — Install Caddy route

Create host match and reverse proxy target.

### Step 7 — Persist state

Write preview metadata atomically.

### Step 8 — Verify route

Check public URL.

Success criteria should be configurable because dev applications may return:

- `200`;
- `301/302`;
- `401`;
- `404` at `/` while still functioning.

For the MVP, consider any valid HTTP response from the public route as proof that routing works.

### Step 9 — Return URL

Human output plus `--json` for coding agents.

---

## 13. Preview Stop Lifecycle

`preview stop pv_k7p2`

1. Resolve state record.
2. Verify caller is allowed to remove it.
3. Delete only that route from Caddy.
4. Verify route is gone.
5. Remove local state record.
6. Return success.

The CLI does **not** kill the application.

That distinction is important:

```text
preview stop = remove public exposure
```

not:

```text
preview stop = kill whatever owns the target port
```

Killing application processes remains the agent/developer's responsibility.

---

## 14. Expiration and Garbage Collection

Every preview should have a default TTL, recommended:

```text
2h
```

This protects against:

- crashed agents;
- forgotten previews;
- abandoned SSH sessions;
- leaked temporary URLs.

### MVP cleanup strategy

Install a systemd timer or cron entry:

```bash
preview gc
```

every few minutes.

Example systemd timer cadence:

```text
every 5 minutes
```

A preview should be garbage-collected if:

- its TTL has expired; or
- its target is unreachable for a configured stale period.

Automatic cleanup should not delete healthy non-expiring previews.

---

## 15. Security Model

### 15.1 Bind applications locally

The recommended application binding is:

```text
127.0.0.1:<port>
```

rather than:

```text
0.0.0.0:<port>
```

Only Caddy needs to be internet-facing.

### 15.2 Caddy Admin API

Must stay on loopback:

```text
127.0.0.1:2019
```

Never expose the Caddy Admin API to the public internet.

### 15.3 Target restrictions

By default the CLI should only proxy to:

```text
127.0.0.1
::1
```

Do not allow:

```text
preview http http://internal-database:5432
```

or arbitrary remote targets in the MVP.

This prevents the CLI from accidentally becoming an SSRF/proxy primitive.

### 15.4 Port restrictions

Allow configurable port ranges.

Potential default:

```text
1024-65535
```

Operators can tighten this.

### 15.5 Preview access protection

Public preview URLs are effectively bearer-by-obscurity if the hostname contains a random ID.

For sensitive environments, future/optional protection can include:

```bash
preview http 3000 --auth basic
preview http 3000 --token
```

Potential mechanisms:

- HTTP Basic Auth;
- generated bearer/cookie token;
- IP allowlist;
- Caddy forward-auth integration.

Authentication should not block MVP delivery.

### 15.6 Secrets

Cloudflare API tokens belong to Caddy/server configuration, not project repositories.

Never write secrets to:

```text
.preview.yaml
```

---

## 16. Cloudflare Setup

Cloudflare is used for DNS only.

Example:

```text
Type:    A
Name:    *.preview
Content: <VPS IP>
Proxy:   DNS only
TTL:     Auto
```

For IPv6, optionally add AAAA.

Caddy's DNS-01 API token should have minimal zone/DNS edit privileges.

The CLI itself should not need Cloudflare API access during normal preview creation.

That is another important separation:

```text
Cloudflare API credentials → Caddy certificate issuance
Caddy Admin API            → preview route management
preview CLI                → local Caddy routing only
```

---

## 17. Caddy Bootstrapping and Managed Configuration

The Caddy configuration required by `preview` is a **built-in part of the CLI**.

Users should not have to copy, understand, or maintain a project-specific Caddyfile. Instead, the CLI embeds a versioned Caddy configuration template and renders the correct configuration from the user's settings.

The intended first-time setup command is:

```bash
sudo preview server init
```

### 17.1 What `preview server init` does

The command should:

1. load an existing `/etc/preview/config.yaml`, or interactively create one;
2. ask only for user-owned values such as domain, DNS provider, credential environment variable, TTL, and optional routing/security settings;
3. verify that Caddy is installed and reachable;
4. verify that the installed Caddy binary includes the required DNS-provider module when wildcard DNS-01 is needed;
5. render the built-in Caddy configuration template;
6. install/update the `preview`-managed Caddy configuration at a known path such as:

```text
/etc/caddy/preview.caddy
```

7. ensure the main Caddy configuration imports the managed file, or use the Admin API where appropriate;
8. validate the generated Caddy configuration before activation;
9. reload Caddy safely;
10. verify the local Caddy Admin API;
11. verify wildcard DNS resolution;
12. verify HTTPS/certificate readiness;
13. initialize `/var/lib/preview`;
14. optionally install/enable the `preview gc` systemd timer;
15. finish by running the equivalent of `preview doctor`.

Example interaction:

```text
$ sudo preview server init

Preview base domain:
> preview.example.com

Hostname template:
> {{project}}--{{name}}--{{id}}.preview.example.com

DNS provider:
> cloudflare

Credential environment variable:
> CLOUDFLARE_API_TOKEN

Default preview lifetime:
> 2h

Caddy Admin API:
> http://127.0.0.1:2019

✓ Configuration written
✓ Caddy DNS provider available
✓ Managed Caddy configuration installed
✓ Caddy configuration valid
✓ Caddy reloaded
✓ Wildcard DNS resolves to this server
✓ HTTPS ready
✓ Preview server ready
```

### 17.2 Embedded Caddy template

Conceptually, the embedded template represents configuration equivalent to:

```caddy
*.preview.example.com {
    tls {
        dns cloudflare {env.CLOUDFLARE_API_TOKEN}
    }

    # preview-managed dynamic routes are inserted through Caddy's Admin API
}
```

However, this is **implementation detail**. A normal user should never need to author this block.

The template should be rendered from configuration, so the same binary can support:

```text
*.preview.example.com
*.dev.company.net
*.my-project.preview.example.com
```

without changing Go source code.

The generated/managed file should contain a clear warning:

```text
# GENERATED BY preview — DO NOT EDIT MANUALLY.
# Change /etc/preview/config.yaml and run:
#   sudo preview server apply
```

### 17.3 Applying configuration changes

Provide:

```bash
sudo preview server apply
```

This command should:

1. re-read `/etc/preview/config.yaml`;
2. render the embedded Caddy template;
3. validate it;
4. atomically replace the managed Caddy configuration;
5. reload Caddy;
6. verify that the preview TLS/routing base is healthy.

This lets users change domains and other settings without editing Caddy directly.

### 17.4 Inspecting generated configuration

For transparency/debugging:

```bash
preview server render
```

prints the Caddy configuration that would be generated without modifying the system.

And:

```bash
preview server apply --dry-run
```

shows the planned changes.

### 17.5 Existing Caddy installations

If the server already runs unrelated Caddy sites, `preview` must not take ownership of the entire Caddy configuration.

It should own only its managed include/snippet and its tagged dynamic preview routes.

The installer should make the smallest necessary integration into the user's existing Caddy setup and leave unrelated routes untouched.

### 17.6 Fresh servers

Caddy remains an external server dependency; the Go binary does not embed the Caddy server itself.

The project may provide an optional install helper for supported Linux distributions, but `preview server init` should primarily configure and validate Caddy rather than silently replacing system packages.

If the installed Caddy binary lacks the configured DNS-provider module, setup should fail with an actionable message explaining how to install a compatible Caddy build.

### 17.7 Ownership boundary

The responsibility split is:

```text
User configuration
    ↓
/etc/preview/config.yaml
    ↓
preview server init/apply
    ↓
embedded versioned Caddy template
    ↓
preview-managed Caddy config
    ↓
Caddy
```

At runtime:

```text
preview http/stop
    ↓
Caddy Admin API
    ↓
temporary hostname → localhost:port routes
```

This keeps Caddy implementation details inside the product while leaving deployment-specific values configurable.

---

## 18. Go Project Structure

Suggested repository:

```text
preview/
├── cmd/
│   └── preview/
│       └── main.go
├── internal/
│   ├── caddy/
│   │   ├── client.go
│   │   ├── routes.go
│   │   └── health.go
│   ├── config/
│   │   ├── config.go
│   │   └── project.go
│   ├── preview/
│   │   ├── create.go
│   │   ├── stop.go
│   │   ├── list.go
│   │   ├── status.go
│   │   └── gc.go
│   ├── hostname/
│   │   ├── template.go
│   │   └── slug.go
│   ├── health/
│   │   └── check.go
│   ├── state/
│   │   ├── store.go
│   │   └── lock.go
│   └── ui/
│       ├── human.go
│       └── json.go
├── deploy/
│   ├── caddy/
│   └── systemd/
├── examples/
│   ├── config.yaml
│   └── project.preview.yaml
├── scripts/
│   └── install.sh
├── .goreleaser.yaml
├── go.mod
├── go.sum
├── LICENSE
├── README.md
└── plan.md
```

Potential CLI libraries:

- Cobra for commands/flags;
- a small YAML library for configuration.

Avoid unnecessary runtime dependencies.

---

## 19. Machine-Friendly Behavior for AI Agents

Agent use is a first-class requirement.

Every important command should support:

```bash
--json
```

Errors should have:

- non-zero exit status;
- concise stderr text;
- optional structured error output.

Example:

```json
{
  "error": {
    "code": "TARGET_UNREACHABLE",
    "message": "nothing is listening on 127.0.0.1:3000"
  }
}
```

Useful exit behavior:

```text
0  success
1  generic error
2  invalid arguments/config
3  target unavailable
4  caddy unavailable
5  hostname/route conflict
```

Exact exit codes can be finalized during implementation.

---

## 20. Friendly Preview Naming

The project name should always be visible unless explicitly disabled.

Default:

```text
<project>--<name>--<id>
```

If no `--name` is supplied:

```text
<project>--<id>
```

Examples:

```text
dashboard--k7p2.preview.example.com
dashboard--oauth-fix--k7p2.preview.example.com
```

The random suffix should always remain present by default to avoid collisions between multiple agents/tasks.

Possible optional deterministic hostname:

```bash
preview http 3000 --name staging --stable
```

Result:

```text
dashboard--staging.preview.example.com
```

This should be opt-in because a stable hostname introduces replacement/concurrency semantics.

---

## 21. Handling Multiple Previews on the Same Port

Multiple hostnames may safely point to one port.

Example:

```text
dashboard--design-a--1234.preview.example.com → 127.0.0.1:3000
dashboard--review--5678.preview.example.com   → 127.0.0.1:3000
```

Stopping one should not affect the other.

Routes are identified by preview ID, not port.

---

## 22. Handling One Project with Multiple Agents

This is a primary expected use case.

Example:

```text
Agent A:
dashboard--auth--k7p2.preview.example.com → :4101

Agent B:
dashboard--billing--4m9x.preview.example.com → :4102

Agent C:
dashboard--ui--p3qd.preview.example.com → :4103
```

The CLI must never assume one preview per project.

Each agent should ideally use a separate Git worktree and app port.

---

## 23. Suggested Global Agent Skill

A reusable global agent skill can contain:

```text
# Preview workflow

When you have finished implementing a requested change and the repository
contains a runnable web application:

1. Start the project's normal development server.
2. Ensure it binds to localhost and identify its HTTP port.
3. Choose a short, DNS-safe name describing the task.
4. Run:
      preview http <port> --name <task-name> --json
5. Verify the command succeeds.
6. Give the returned HTTPS URL to the user.
7. Keep the app process and preview running while awaiting review.
8. Do not commit or push merely because the preview succeeded.
9. If the user approves:
      preview stop <id>
   stop the development server, run the repository's required checks,
   commit, and push according to the user's normal Git workflow.
10. If the user requests changes, keep working and reuse or recreate the
    preview as appropriate.
11. Never expose database ports, admin interfaces, or non-HTTP services
    through preview.
```

This skill should reference only the CLI contract, not Caddy internals.

---

## 24. Installation Experience

Goal:

```bash
curl -fsSL https://.../install.sh | sh
```

or package-manager/release installation.

Preferred release artifacts:

```text
preview_linux_amd64
preview_linux_arm64
```

Use GoReleaser to publish GitHub Releases.

Possible future package formats:

- `.deb`
- `.rpm`
- Homebrew tap

### First-time setup

The intended setup experience is:

```bash
sudo preview server init
```

The CLI should create the required directories, build the global configuration interactively (or from flags), render its built-in Caddy template, validate/reload Caddy, and run health checks.

Non-interactive setup should also be supported for provisioning tools:

```bash
sudo preview server init   --domain preview.example.com   --dns-provider cloudflare   --dns-credentials-env CLOUDFLARE_API_TOKEN   --default-ttl 2h   --non-interactive
```

After initial setup:

```bash
preview doctor
```

can be run at any time.

If the operator later changes `/etc/preview/config.yaml`, apply the generated infrastructure configuration with:

```bash
sudo preview server apply
```

Users should not need to manually create `/etc/preview`, `/var/lib/preview`, or a Caddy snippet in the normal installation path.

The README should still document DNS and Caddy prerequisites so the automation is understandable and debuggable.

---

## 25. Observability and Logs

Human output should be concise.

Verbose troubleshooting:

```bash
preview http 3000 --debug
```

Debug output can include:

- resolved config;
- target probe;
- hostname generation;
- Caddy API request stages;
- public health check;
- state path.

Do not log credentials or authorization headers.

Optionally use structured logs internally.

---

## 26. Failure Handling

### Nothing listening on port

Fail before creating a route.

### Caddy unavailable

Do not write preview state as active.

### Route created but state write fails

Attempt to roll back the Caddy route.

### State written but public verification fails

Report failure and clean up unless:

```bash
--keep-on-failure
```

is explicitly provided.

### Preview already gone

`preview stop` should be idempotent where practical.

If state exists but the Caddy route is gone:

- clean stale state;
- return success with warning.

### Caddy has route but local state is missing

`preview gc` / `preview doctor` should be able to identify orphaned routes tagged as belonging to `preview`.

---

## 27. Testing Strategy

### Unit tests

Cover:

- slug generation;
- hostname templates;
- DNS label limits;
- unique IDs;
- config precedence;
- TTL parsing;
- state locking;
- stale-state decisions.

### Integration tests

Run Caddy in a container or test process and verify:

- route creation;
- proxying;
- route removal;
- concurrent previews;
- duplicate ports;
- duplicate names;
- cleanup.

### End-to-end test

Example:

1. start a tiny local HTTP server on random port;
2. create preview;
3. request host through Caddy;
4. confirm response;
5. stop preview;
6. confirm route no longer resolves through Caddy.

DNS/real certificate tests should be separate from normal CI.

---

## 28. MVP Scope

Version `v0.1.0` should include:

- `preview http <port>`;
- `--name`;
- `--project`;
- project auto-detection;
- project-aware hostnames;
- configurable hostname template;
- `preview stop`;
- `preview list`;
- `preview status`;
- `preview init`;
- `preview doctor`;
- `preview gc`;
- TTL;
- JSON output;
- embedded Caddy configuration template;
- `preview server init`, `server apply`, and `server render`;
- Caddy Admin API integration;
- local JSON state store;
- Cloudflare + Caddy setup automation/documentation;
- systemd garbage-collection timer example;
- Linux amd64/arm64 binaries;
- GoReleaser release workflow.

Do not add authentication, Docker management, Git automation, or tunnels to v0.1 unless needed during real-world testing.

---

## 29. Post-MVP Features

Potential roadmap:

### v0.2

- Basic Auth previews;
- stable named previews;
- better stale-target detection;
- shell completions;
- SQLite state backend;
- richer `preview doctor`;
- per-user ownership rules.

### v0.3

- optional access tokens;
- IP allowlists;
- custom headers;
- WebSocket-specific tests;
- SSE validation;
- project aliases;
- audit log.

### Later

- optional remote agent/client mode for machines behind NAT;
- multi-server control plane;
- SSH-based remote forwarding;
- dashboard;
- API;
- OAuth/SSO;
- automatic GitHub PR comments.

These should remain optional so the core CLI stays small.

---

## 30. Important Web Framework Compatibility

Because Caddy is performing a normal HTTP reverse proxy, the CLI should work with:

- Next.js / T3;
- Vite;
- React;
- Astro;
- SvelteKit;
- Nuxt;
- Django;
- Flask;
- FastAPI;
- Rails;
- Laravel;
- Go HTTP servers;
- Rust web servers;
- local Docker-published HTTP ports.

Caddy should preserve WebSocket upgrade behavior so Next.js/Vite hot reload continues to work through the preview URL.

Potential framework-specific issue:

Some dev servers restrict `Host` headers or allowed origins.

That is not the CLI's responsibility to solve automatically. Documentation should include a troubleshooting section describing how to allow the generated preview domain in frameworks that enforce host/origin restrictions.

---

## 31. Recommended User Experience

### Once per server

After installing a compatible Caddy build and the `preview` binary:

```bash
sudo preview server init
```

The user supplies the domain and a small set of environment-specific values. The CLI installs its own generated Caddy configuration and validates the server.

If the domain or other server settings later change:

```bash
sudo preview server apply
```

No manual Caddy editing should be required in the normal workflow.

### Once per project

```bash
preview init
```

Then during normal development:

```bash
preview http 3000 --name auth-fix
```

Output:

```text
✓ Target reachable: http://127.0.0.1:3000
✓ Route created
✓ HTTPS reachable

https://dashboard--auth-fix--k7p2.preview.example.com

Expires in 2h
Preview ID: pv_k7p2
```

Cleanup:

```bash
preview stop pv_k7p2
```

Output:

```text
✓ Preview pv_k7p2 stopped
```

That is the product.

---

## 32. Recommended Implementation Order

### Phase 1 — CLI foundation

Implement:

- command framework;
- global config;
- `.preview.yaml`;
- project detection;
- slug/ID generation;
- hostname template system;
- JSON/human output.

### Phase 2 — Managed Caddy integration

Implement:

- embedded/versioned Caddy configuration template;
- `preview server init`;
- `preview server render`;
- `preview server apply`;
- configuration validation and safe reload;
- detection of required DNS-provider module;
- Caddy Admin API client;
- route creation;
- route deletion;
- stable route IDs;
- local health checks;
- protection of unrelated existing Caddy routes/configuration.

Test with HTTP first, then wildcard TLS.

### Phase 3 — State and lifecycle

Implement:

- state store;
- locking;
- `list`;
- `status`;
- TTL;
- `gc`;
- rollback behavior.

### Phase 4 — TLS, server setup, and documentation

Implement/document:

- wildcard DNS;
- DNS-only Cloudflare;
- Caddy Cloudflare DNS module;
- ACME wildcard certificate;
- least-privilege Cloudflare API token;
- interactive and non-interactive `preview server init`;
- generated Caddy config ownership;
- `preview server apply` after configuration changes;
- `preview server render` / dry-run troubleshooting;
- firewall recommendations.

### Phase 5 — Agent ergonomics

Add:

- `--json`;
- deterministic error codes;
- concise output;
- global skill example;
- examples for multiple simultaneous agents.

### Phase 6 — Packaging

Add:

- GoReleaser;
- GitHub Actions;
- checksums;
- Linux amd64/arm64 builds;
- install script;
- systemd GC timer.

---

## 33. Design Principles to Preserve

As the project grows, keep these boundaries:

### `preview` exposes ports; it does not run projects.

```text
GOOD:
preview http 3000

AVOID:
preview run pnpm dev
```

### `preview` manages networking; it does not manage Git.

```text
GOOD:
agent → preview → user reviews → agent commits

AVOID:
preview approve --commit --push
```

### Caddy owns TLS; `preview` owns Caddy configuration for preview infrastructure.

```text
GOOD:
user changes /etc/preview/config.yaml
→ preview server apply
→ preview renders its built-in Caddy template
→ Caddy obtains and renews certificates

AVOID:
user manually maintains preview-specific Caddy routes
AVOID:
preview shells out to Certbot
```

Caddy remains an external runtime dependency. The distinction is that users configure `preview`, and `preview` generates/manages the Caddy configuration it owns.

### Cloudflare is DNS-only for preview traffic.

```text
Browser → Caddy directly
```

### Project identity appears in the URL.

Default:

```text
<project>--<task>--<id>.preview.example.com
```

### Everything important should work non-interactively.

This is essential for AI-agent usage.

---

## 34. Example End-to-End Session

Developer/agent:

```bash
cd /srv/projects/dashboard

pnpm dev --port 4103
```

Application:

```text
Ready on http://127.0.0.1:4103
```

Agent:

```bash
preview http 4103 --name profile-page --json
```

Response:

```json
{
  "id": "pv_a82f",
  "project": "dashboard",
  "url": "https://dashboard--profile-page--a82f.preview.example.com",
  "target": "http://127.0.0.1:4103",
  "expires_in": "2h"
}
```

Agent tells the user:

```text
The implementation is ready to review:
https://dashboard--profile-page--a82f.preview.example.com
```

User approves.

Agent:

```bash
preview stop pv_a82f
```

Then:

```bash
# stop dev server
pnpm lint
pnpm test
git add -A
git commit -m "feat: update profile page"
git push
```

The public preview URL is now dead, and Git operations remain fully independent of the preview infrastructure.

---

## 35. Final Recommended MVP Architecture

```text
                         Cloudflare
                         DNS only
                            │
            *.preview.example.com → VPS
                            │
                            ▼
                     ┌────────────┐
                     │   Caddy    │
                     │ HTTPS/TLS  │
                     └─────┬──────┘
                           │
                    dynamic routes
                           │
               ┌───────────┼────────────┐
               │           │            │
               ▼           ▼            ▼
           :3000        :4101        :5173
         T3/Next       Django         Vite
               ▲           ▲            ▲
               │           │            │
         coding agents start apps normally

                  preview CLI
                       │
            ┌──────────┴──────────┐
            ▼                     ▼
      Caddy Admin API       local state/TTL
      127.0.0.1:2019
```

The core contract remains:

```bash
preview http <port>
```

and returns:

```text
https://<project>--<name>--<id>.preview.example.com
```

This gives coding agents a universal preview mechanism without coupling the tool to any particular framework, package manager, application command, Git workflow, or deployment platform.
