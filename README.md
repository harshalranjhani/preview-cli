# preview

`preview` publishes a local HTTP port as a temporary HTTPS hostname on a domain you control. You start the app yourself. The CLI adds a Caddy route for it. Stopping the preview removes that public URL and leaves the app running.

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
preview stop k7p2
```

Any HTTP server on localhost works: Next.js, Vite, Django, Rails, Go, or a published Docker port. The CLI does not start the app, install dependencies, or touch git.

## Requirements

- A Linux server (amd64 or arm64) where Caddy can listen on ports 80 and 443
- A domain hosted on Cloudflare
- A Cloudflare API token with DNS edit permission on that zone

Caddy obtains the wildcard certificate from Let's Encrypt. The token is used only for the DNS challenge. It does not create DNS records.

## Install

On the server:

```bash
curl -fsSL https://raw.githubusercontent.com/harshalranjhani/preview-cli/refs/heads/main/scripts/install.sh | sh
preview version
```

The script installs `preview` to `/usr/local/bin/preview` and a Caddy build that includes the Cloudflare DNS module. The stock Caddy package cannot issue wildcard certificates. The Caddy download is built on demand and can take about a minute.

Later releases:

```bash
sudo preview update
```

## DNS

If the domain is `example.com`, use `preview.example.com` as the base domain. Create a DNS-only wildcard record for that name. On Cloudflare, leave the cloud grey so Cloudflare does not proxy the hostname.

```text
*.preview.example.com.    A       203.0.113.10
```

Add an AAAA record when the server has a public IPv6 address. The record name is `*.preview.example.com`, not `*.example.com`. The value in `preview` config is `preview.example.com`, without the asterisk.

Open TCP ports 80 and 443 to the internet. Caddy serves HTTPS on 443 and redirects HTTP on 80.

## Server setup

```bash
sudo preview server init
```

The prompts ask for:

| Setting | Example | Purpose |
| --- | --- | --- |
| Base domain | `preview.example.com` | Hostnames look like `app--task--k7p2.preview.example.com` |
| Public IPv4 | `203.0.113.10` | Checked against the wildcard DNS record |
| Public IPv6 | optional | Checked when you publish an AAAA record |
| ACME email | `you@example.com` | Let's Encrypt account contact. It is not shown on the certificate |
| DNS provider | `cloudflare` | Module Caddy uses for the DNS challenge |
| Credentials env | `CLOUDFLARE_API_TOKEN` | Name of the variable, not the token |
| DNS token | pasted once | Written to `/etc/preview/dns.env` |
| Default lifetime | `2h` | Forgotten previews expire on their own |

Press Enter to keep the value shown in brackets. Ctrl-C during init saves nothing. After setup, edit `/etc/preview/config.yaml` and run `sudo preview server apply`. To change the token, edit `/etc/preview/dns.env` and run `sudo systemctl restart caddy`.

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

Init writes `/etc/preview/config.yaml`, installs `/etc/caddy/preview.caddy`, imports that file from the existing Caddyfile, and enables a timer that runs `preview gc` every five minutes.

Check the server:

```bash
preview doctor
```

`preview server render` prints the generated Caddy site without writing it.

## Day to day

From the app directory, with the app already listening:

```bash
preview http 3000 --name auth-fix
preview list
preview status k7p2
preview stop k7p2
```

`stop` and `status` accept a full id (`pv_k7p2`), the short id (`k7p2`), or a unique prefix of either. If the prefix matches more than one preview, the command names them and does nothing.

```bash
preview http 3000 --project dashboard --name auth-fix --ttl 30m
preview http 3000 --ttl 0          # no expiry
preview http 3000 --json           # for scripts and agents
preview stop --project             # current project only
preview stop --all                 # this user's previews; root stops every preview
preview gc                         # remove expired routes now
preview gc --dry-run
```

Several previews may point at the same port. Each gets its own hostname. Stopping one does not stop the app or the other previews.

`preview status` reports `expired` as soon as the lifetime ends. The public URL stays up until garbage collection deletes the route. The timer runs every five minutes. `sudo preview gc` removes expired routes immediately.

To run cleanup more often, edit `/etc/systemd/system/preview-gc.timer` and set `OnUnitActiveSec` (for example `1min`), then:

```bash
sudo systemctl daemon-reload
sudo systemctl restart preview-gc.timer
```

`sudo preview server init` writes that timer again and restores the five-minute interval.

`preview init` writes `.preview.yaml` in the git root. That file may be committed. It never contains the DNS token.

```yaml
project: dashboard
```

Default hostname shape:

```text
<project>--<name>--<id>.<base-domain>
```

Change `server.hostname_template` when you want a different shape. It must include `{{id}}`. Placeholders are `{{project}}`, `{{name}}`, `{{id}}`, and `{{base_domain}}`.

## Update and uninstall

```bash
sudo preview update
```

```bash
sudo preview uninstall
sudo preview uninstall --caddy   # also stop Caddy and remove its binary
```

Uninstall removes preview routes, `/etc/preview`, `/var/lib/preview`, the systemd units, and the `preview` binary. Other sites in the Caddyfile are left in place. `sudo preview uninstall --yes` skips the confirmation prompt.

## Configuration

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

| Path | Contents |
| --- | --- |
| `/etc/preview/config.yaml` | Domain, IPs, TTL, Caddy paths |
| `/etc/preview/dns.env` | `CLOUDFLARE_API_TOKEN=...`, mode `0600` |
| `/etc/caddy/preview.caddy` | Generated wildcard site |
| `/var/lib/preview/state.json` | Current preview routes |

`dns.credentials_env` is the variable name. The secret lives only in `dns.env`.

## Build

```bash
go build -o preview ./cmd/preview
go test ./...
```

Release binaries for Linux amd64 and arm64 are published on git tags. `scripts/install.sh` installs the latest release.

## Troubleshooting

**The preview URL loads, but the dev server rejects it.** Some frameworks refuse unknown `Host` values. Allow the preview domain in the app. For Vite, set `server.allowedHosts: true`. For Create React App, set `DANGEROUSLY_DISABLE_HOST_CHECK=true`. Django uses `ALLOWED_HOSTS`. Rails uses `config.hosts`. Bind the app to `127.0.0.1`. Only Caddy needs to listen on the public interface.

**`preview doctor` says the wildcard does not resolve.** The DNS record must be `*.preview.example.com` when the base domain is `preview.example.com`, DNS-only, and aimed at `server.public_ip`. Certificate issuance can take a minute after DNS is visible.

**Caddy admin is not reachable.** The admin API stays on `127.0.0.1:2019`. Do not expose it publicly. `preview doctor` fails when Caddy is not running or the admin endpoint is bound to any other address. `sudo systemctl status caddy` shows why the process exited. Port 80 or 443 already in use is a common cause.

**Routes disappear after a Caddy reload.** `preview server apply` and the `preview-restore` service put saved routes back. `preview server sync` does the same thing on demand.

**A preview outlived its task.** The default lifetime is 2 hours. `preview gc` also removes previews whose local port has been down for `preview.stale_after` (default 10 minutes). Previews created with `--ttl 0` stay until you stop them or the port dies.
