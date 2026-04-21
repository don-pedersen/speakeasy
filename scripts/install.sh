#!/usr/bin/env bash
#
# One-shot Speakeasy installer for Debian/Ubuntu VPSes.
#
# What it does:
#   - Installs Go (latest stable) if missing or too old
#   - Clones speakeasy, builds the binary, installs to /usr/local/bin
#   - Creates the `speakeasy` system user + /etc/speakeasy, /var/lib/speakeasy
#   - Installs a hardened systemd unit (not enabled; you enable it after editing config)
#   - Drops a commented example config at /etc/speakeasy/config.toml.example
#   - Adds the invoking user to the `speakeasy` group so they can run the CLI
#
# What it does NOT do:
#   - Start the daemon (you configure first)
#   - Install Caddy / Headscale (see docs/deploy-home-through-vps.md for the
#     full self-hosted tunnel setup)
#   - Configure DNS, TLS, firewall rules, or routes
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/don-pedersen/speakeasy/main/scripts/install.sh | sudo bash
# or:
#   sudo ./scripts/install.sh

set -euo pipefail

MIN_GO_VERSION="1.25"
REPO_URL="${SPEAKEASY_REPO:-https://github.com/don-pedersen/speakeasy.git}"
REPO_REF="${SPEAKEASY_REF:-main}"
PREFIX="${PREFIX:-/usr/local}"
BIN="$PREFIX/bin/speakeasy"
UNIT="/etc/systemd/system/speakeasy.service"
CONF_DIR="/etc/speakeasy"
DATA_DIR="/var/lib/speakeasy"

log()   { printf "\033[1;32m==>\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m!! \033[0m %s\n" "$*" >&2; }
die()   { printf "\033[1;31mxx \033[0m %s\n" "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "Run with sudo (or as root)."
[ -f /etc/debian_version ] || warn "Not Debian/Ubuntu — script may work but is untested elsewhere."

# ---------------------------------------------------------------------------
# Go
# ---------------------------------------------------------------------------

install_go() {
  log "Installing Go (latest stable)…"
  local go_ver
  go_ver=$(curl -fsSL https://go.dev/VERSION?m=text | head -1)
  local tarball="${go_ver}.linux-amd64.tar.gz"
  local tmp
  tmp=$(mktemp -d)
  (
    cd "$tmp"
    curl -fsSLO "https://go.dev/dl/${tarball}"
    file "$tarball" | grep -q gzip || die "Go download wasn't a gzip — check your network."
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "$tarball"
  )
  rm -rf "$tmp"
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  export PATH="$PATH:/usr/local/go/bin"
  log "Installed $(go version)"
}

need_go() {
  if ! command -v go >/dev/null 2>&1; then return 0; fi
  local cur
  cur=$(go version | awk '{print $3}' | sed 's/^go//')
  # crude version compare
  [ "$(printf '%s\n' "$MIN_GO_VERSION" "$cur" | sort -V | head -1)" = "$MIN_GO_VERSION" ] && return 1
  return 0
}

if need_go; then install_go; fi
export PATH="$PATH:/usr/local/go/bin"

# ---------------------------------------------------------------------------
# Dependencies
# ---------------------------------------------------------------------------

log "Installing build prerequisites…"
apt-get update -qq
apt-get install -y -qq git curl ca-certificates >/dev/null

# ---------------------------------------------------------------------------
# Build + install
# ---------------------------------------------------------------------------

BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT

log "Cloning $REPO_URL@$REPO_REF…"
git clone --depth 1 --branch "$REPO_REF" "$REPO_URL" "$BUILD_DIR/speakeasy"

log "Building binary…"
(cd "$BUILD_DIR/speakeasy" && go build -o speakeasy ./cmd/speakeasy)

log "Installing $BIN"
install -m 0755 "$BUILD_DIR/speakeasy/speakeasy" "$BIN"

# ---------------------------------------------------------------------------
# User + dirs
# ---------------------------------------------------------------------------

if ! id -u speakeasy >/dev/null 2>&1; then
  log "Creating 'speakeasy' system user"
  useradd -r -s /usr/sbin/nologin -d "$DATA_DIR" speakeasy
fi

mkdir -p "$CONF_DIR" "$DATA_DIR"
chown -R speakeasy:speakeasy "$DATA_DIR"
chmod 0750 "$DATA_DIR"

# Make the config dir group-readable so the service user can load config.
chgrp speakeasy "$CONF_DIR"
chmod 0750 "$CONF_DIR"

# Example config — never clobber an existing live config
install -m 0640 -o root -g speakeasy \
  "$BUILD_DIR/speakeasy/packaging/config.toml.example" \
  "$CONF_DIR/config.toml.example"

# ---------------------------------------------------------------------------
# Add invoking user to the speakeasy group
# ---------------------------------------------------------------------------

ORIG_USER="${SUDO_USER:-}"
if [ -n "$ORIG_USER" ] && [ "$ORIG_USER" != "root" ]; then
  if ! id -nG "$ORIG_USER" | tr ' ' '\n' | grep -qx speakeasy; then
    log "Adding '$ORIG_USER' to speakeasy group (log out + back in to take effect)"
    usermod -aG speakeasy "$ORIG_USER"
  fi
fi

# ---------------------------------------------------------------------------
# systemd unit (hardened)
# ---------------------------------------------------------------------------

log "Writing systemd unit $UNIT"
cat > "$UNIT" <<'UNIT'
[Unit]
Description=Speakeasy Gateway
Documentation=https://github.com/don-pedersen/speakeasy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=speakeasy
Group=speakeasy
ExecStart=/usr/local/bin/speakeasy --config /etc/speakeasy/config.toml serve
Restart=on-failure
RestartSec=5

# Allow binding 80/443 without running as root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# /run/speakeasy/speakeasy.sock lives here; systemd creates the dir.
RuntimeDirectory=speakeasy
RuntimeDirectoryMode=0755

# Sandboxing
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/var/lib/speakeasy
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX
RestrictNamespaces=true
LockPersonality=true

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload

# ---------------------------------------------------------------------------
# Done
# ---------------------------------------------------------------------------

cat <<SUMMARY

$(printf '\033[1;32m==> speakeasy installed\033[0m')

Next steps:
  1. Generate a starter config (interactive prompts, or all-flags for automation):
       sudo speakeasy init
         # or, unattended:
       sudo speakeasy init --domain example.com \\
         --route-name app --route-path / --route-upstream http://localhost:8080
  2. (optional) enable the /admin web panel:
       sudo -u speakeasy speakeasy --config $CONF_DIR/config.toml admin set-password
  3. Start the gateway:
       sudo systemctl enable --now speakeasy
       sudo journalctl -u speakeasy -f                 # watch it boot
  4. Log out + back in so your shell picks up the 'speakeasy' group, then:
       speakeasy --config $CONF_DIR/config.toml token mint --label alice --route <name> --expires 7d

Docs:
  - README                             https://github.com/don-pedersen/speakeasy
  - home -> VPS -> invited-user guide  docs/deploy-home-through-vps.md
  - Fully self-hosted (Caddy+Headscale)  same doc, Appendix at the bottom
SUMMARY
