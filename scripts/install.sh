#!/bin/sh
# Install preview and a Caddy build with the Cloudflare DNS module.
#   curl -fsSL https://raw.githubusercontent.com/harshalranjhani/preview-cli/refs/heads/main/scripts/install.sh | sh
set -eu

repo="${PREVIEW_REPO:-harshalranjhani/preview-cli}"
version="${PREVIEW_VERSION:-latest}"
dest="${PREVIEW_INSTALL_DIR:-/usr/local/bin}"
caddy_plugin="${PREVIEW_CADDY_PLUGIN:-github.com/caddy-dns/cloudflare}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

if [ "$os" != "linux" ]; then
  echo "The release binary is built for Linux. On this machine, build from source:" >&2
  echo "  go build -o preview ./cmd/preview" >&2
  exit 1
fi

run_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  else
    sudo "$@"
  fi
}

if [ "$version" = "latest" ]; then
  preview_url="https://github.com/${repo}/releases/latest/download/preview_${os}_${arch}"
else
  preview_url="https://github.com/${repo}/releases/download/${version}/preview_${os}_${arch}"
fi

tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT
echo "Downloading ${preview_url}"
curl -fsSL --retry 3 --retry-delay 2 "$preview_url" -o "$tmp"
chmod 755 "$tmp"
run_root mkdir -p "$dest"
run_root mv "$tmp" "${dest}/preview"
trap - EXIT
echo "Installed ${dest}/preview"

if command -v caddy >/dev/null 2>&1 && caddy list-modules 2>/dev/null | grep -q 'dns.providers.cloudflare'; then
  echo "Caddy already includes the Cloudflare DNS module"
else
  caddy_url="https://caddyserver.com/api/download?os=linux&arch=${arch}&p=${caddy_plugin}"
  echo "Downloading Caddy with the Cloudflare DNS module"
  echo "This build is created on demand and can take a minute."
  caddy_tmp=$(mktemp)
  trap 'rm -f "$caddy_tmp"' EXIT
  curl -fL --retry 5 --retry-delay 5 --max-time 300 "$caddy_url" -o "$caddy_tmp"
  chmod 755 "$caddy_tmp"
  run_root mv "$caddy_tmp" /usr/bin/caddy
  trap - EXIT
  echo "Installed /usr/bin/caddy"
fi

if ! id caddy >/dev/null 2>&1; then
  echo "Creating the caddy service user"
  if command -v useradd >/dev/null 2>&1; then
    run_root useradd --system --home /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
  else
    run_root adduser --system --home /var/lib/caddy --shell /usr/sbin/nologin caddy
  fi
fi
run_root mkdir -p /etc/caddy /var/lib/caddy
run_root chown caddy:caddy /var/lib/caddy

if [ ! -f /etc/caddy/Caddyfile ]; then
  caddyfile=$(mktemp)
  cat > "$caddyfile" <<'EOF'
{
	admin 127.0.0.1:2019
}

:80 {
	respond "Caddy is installed. Run: sudo preview server init" 404
}
EOF
  run_root mv "$caddyfile" /etc/caddy/Caddyfile
  run_root chmod 644 /etc/caddy/Caddyfile
fi

if ! command -v systemctl >/dev/null 2>&1; then
  echo "systemd was not found. Start Caddy yourself, then run: sudo preview server init"
  exit 0
fi

if [ ! -f /lib/systemd/system/caddy.service ] && [ ! -f /usr/lib/systemd/system/caddy.service ] && [ ! -f /etc/systemd/system/caddy.service ]; then
  unit=$(mktemp)
  cat > "$unit" <<'EOF'
[Unit]
Description=Caddy
Documentation=https://caddyserver.com/docs/
After=network.target network-online.target
Requires=network-online.target

[Service]
Type=notify
User=caddy
Group=caddy
ExecStart=/usr/bin/caddy run --environ --config /etc/caddy/Caddyfile --adapter caddyfile
ExecReload=/usr/bin/caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile --force
TimeoutStopSec=5s
LimitNOFILE=1048576
AmbientCapabilities=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
EOF
  run_root mv "$unit" /etc/systemd/system/caddy.service
  run_root chmod 644 /etc/systemd/system/caddy.service
  run_root systemctl daemon-reload
fi

bind=$(mktemp)
cat > "$bind" <<'EOF'
[Service]
NoNewPrivileges=false
AmbientCapabilities=CAP_NET_BIND_SERVICE
EOF
run_root mkdir -p /etc/systemd/system/caddy.service.d
run_root mv "$bind" /etc/systemd/system/caddy.service.d/preview-bind.conf
run_root chmod 644 /etc/systemd/system/caddy.service.d/preview-bind.conf
run_root systemctl daemon-reload

if run_root systemctl is-active --quiet caddy; then
  run_root systemctl restart caddy
else
  run_root systemctl enable --now caddy
fi

echo "Installed Caddy with the Cloudflare DNS module"
echo "Next: sudo preview server init"
