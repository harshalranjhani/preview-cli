# preview

`preview` publishes a local HTTP port on a temporary HTTPS hostname under a domain you control. Point the app at a port you already started, and the CLI adds a Caddy route for it. Stopping the preview removes that public URL and leaves the app running.

```bash
preview http 3000 --name auth-fix
```

```text
✓ Target reachable: http://127.0.0.1:3000
✓ Route created
✓ HTTPS reachable

https://dashboard--auth-fix--k7p2.preview.example.com

Expires in 2h
Preview ID: pv_k7p2
```

```bash
preview stop pv_k7p2
```

The tool does not start your app, install dependencies, or touch git. Any HTTP server on localhost works: Next.js, Vite, Django, Rails, Go, or a published Docker port.

## What you set

Server setup asks for a few values and writes them to `/etc/preview/config.yaml`. You do not write Caddy routes or TLS config yourself.

| Setting | Example | Purpose |
| --- | --- | --- |
| Base domain | `preview.example.com` | Hostnames look like `app--task--k7p2.preview.example.com` |
| Public IPv4 | `203.0.113.10` | Checked against the wildcard DNS record |
| Public IPv6 | optional | Checked when you publish an AAAA record |
| DNS provider | `cloudflare` | Caddy uses it to issue the wildcard certificate |
| DNS token | env file only | Stored in `/etc/preview/dns.env`, never in the repo |
| Default lifetime | `2h` | Forgotten previews expire on their own |

A project can optionally add `.preview.yaml` with just its name:

```yaml
project: dashboard
```

## One-time server setup

1. Install [Caddy](https://caddyserver.com/docs/install) built with your DNS provider. For Cloudflare:

   ```bash
   xcaddy build --with github.com/caddy-dns/cloudflare
   ```

   A custom build from [caddyserver.com/download](https://caddyserver.com/download) works too. The stock package does not include the Cloudflare DNS module, which wildcard certificates need.

2. Create a **DNS-only** record at your DNS host. On Cloudflare, leave the cloud grey so Cloudflare does not proxy the hostname. Caddy terminates HTTPS.

   ```text
   *.preview.example.com.   A      203.0.113.10
   ```

   Add an AAAA record as well when this server has a public IPv6 address. Open TCP port 443 to the internet.

3. Install the `preview` binary and bootstrap the server:

   ```bash
   sudo preview server init
   ```

   Non-interactive setup:

   ```bash
   sudo preview server init \
     --non-interactive \
     --domain preview.example.com \
     --public-ip 203.0.113.10 \
     --acme-email you@example.com \
     --dns-provider cloudflare \
     --dns-credentials-env CLOUDFLARE_API_TOKEN \
     --dns-token "$CLOUDFLARE_API_TOKEN" \
     --default-ttl 2h
   ```

   This writes `/etc/preview/config.yaml`, installs a managed Caddy site at `/etc/caddy/preview.caddy`, imports that file from your existing Caddyfile without replacing other sites, and enables a timer that runs `preview gc` every five minutes.

4. Check the server any time:

   ```bash
   preview doctor
   ```

After the first setup, changing the domain or IP is:

```bash
sudo preview server apply
```

`preview server render` prints the generated Caddy site without writing it.

The Cloudflare API token only needs DNS edit permission on the zone. Put it in `/etc/preview/dns.env`:

```text
CLOUDFLARE_API_TOKEN=your-token
```

`server init` installs a systemd drop-in so the Caddy service loads that file. The token is not stored in `config.yaml` or in project files.

## Day to day

From the app directory, with the app already listening:

```bash
preview http 3000 --name auth-fix
preview list
preview status pv_k7p2
preview stop pv_k7p2
```

Useful flags:

```bash
preview http 3000 --project dashboard --name auth-fix --ttl 30m
preview http 3000 --ttl 0          # no expiry
preview http 3000 --json           # for scripts and agents
preview stop --project             # current project only
preview stop --all                 # this user's previews; root stops every preview
preview gc --dry-run
```

`preview init` writes `.preview.yaml` in the git root. That file may be committed. It never contains server credentials.

Several previews may point at the same port. Stopping one does not affect the others. Hostnames keep a random id so two tasks in the same project do not collide.

Default hostname shape:

```text
<project>--<name>--<id>.<base-domain>
```

Change `server.hostname_template` when you want a different shape. It must include `{{id}}`. Placeholders are `{{project}}`, `{{name}}`, `{{id}}`, and `{{base_domain}}`.

## Configuration order

CLI flags win, then environment variables, then `.preview.yaml`, then `/etc/preview/config.yaml`, then built-in defaults.

```text
PREVIEW_CONFIG
PREVIEW_BASE_DOMAIN
PREVIEW_PUBLIC_IP
PREVIEW_CADDY_ADMIN_URL
PREVIEW_STATE_PATH
PREVIEW_PROJECT
PREVIEW_DEFAULT_TTL
```

An example server file is in [`examples/config.yaml`](examples/config.yaml).

## Build

```bash
go build -o preview ./cmd/preview
go test ./...
```

Release binaries for Linux amd64 and arm64 are published with GoReleaser. `scripts/install.sh` downloads the latest Linux binary to `/usr/local/bin/preview`.

## Troubleshooting

**The preview URL loads, but the dev server rejects it.** Some frameworks refuse unknown `Host` values. Allow the preview domain in the app, for example Vite `server.allowedHosts`, Django `ALLOWED_HOSTS`, or Rails `config.hosts`. Bind the app to `127.0.0.1`. Only Caddy needs to listen on the public interface.

**`preview doctor` says the wildcard does not resolve.** The DNS record must be `*.your-base-domain`, DNS-only, and aimed at `server.public_ip`. Certificate issuance can take a minute after DNS is visible.

**Caddy admin is not reachable.** The admin API stays on `127.0.0.1:2019`. Do not expose it publicly. `preview doctor` fails when it is bound to any other address.

**Routes disappear after a Caddy reload.** `preview server apply` and the `preview-restore` service put saved routes back. `preview server sync` does the same thing on demand.

**A preview outlived its task.** The default lifetime is 2 hours. `preview gc` also removes previews whose local port has been down for `preview.stale_after` (default 10 minutes). Previews created with `--ttl 0` stay until you stop them or the port dies.
