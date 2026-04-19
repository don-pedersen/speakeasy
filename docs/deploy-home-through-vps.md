# Deployment: home app → VPS → invited user

Share a web app that lives on your home machine with specific invited people
on the internet, without:

- Opening any inbound port on your home network
- Depending on a dynamic/public IP at home
- Giving recipients an account on anything

```
  Invited user's browser
         │  https://share.example.com/invite?token=...
         ▼
   ┌─────────────────┐
   │  VPS            │   <-- Tailscale / WireGuard tunnel -->
   │  speakeasy      │ ───────────────────────────────────┐
   │  TLS + admin    │                                    │
   └─────────────────┘                                    ▼
                                                   ┌─────────────┐
                                                   │ Home box    │
                                                   │ app :3000   │
                                                   └─────────────┘
```

The VPS is the only thing with a public IP. The home box is reachable only
from the VPS via an encrypted overlay tunnel.

Approximate time: 30–45 minutes if you've done VPS admin before.

---

## What you'll need

- A VPS with a public IPv4 — any provider (Hetzner, DigitalOcean, DartNode,
  Linode…). 1 vCPU, 512 MB RAM, 10 GB disk is plenty.
- A domain name (or a subdomain) you can point at the VPS. This guide uses
  `share.example.com`; substitute your own.
- A home box (Linux, macOS, or Windows with WSL) running the app you want to
  share. This guide assumes it listens on `localhost:3000`.
- A free [Tailscale](https://tailscale.com) account (the easiest tunnel;
  alternatives in the appendix).
- Shell access to both boxes with sudo/admin.

---

## Step 1 — VPS base setup

SSH into the VPS and bring it up to date.

```shell
ssh root@your-vps
apt update && apt upgrade -y
apt install -y git curl ca-certificates
```

Create an unprivileged user if you don't already have one, and log in as them
from here on:

```shell
adduser don && usermod -aG sudo don
# log out, log back in as don
```

### Point DNS at the VPS

You need `share.example.com` (or whatever hostname you choose) to resolve to
the VPS's public IP. The quick path depends on where your domain lives.

#### Using DartNode

DartNode runs its own DNS on PowerDNS (nameservers `ns1.web.dartnode.net`,
`ns2.web.dartnode.net`). You have three options, in order of least friction:

**Easiest — a free `dart.page` subdomain.** DartNode gives you free
subdomains under `dart.page` for any VPS you run with them. In the dashboard
at `https://dartnode.com/app` (older UI) or `https://dartnode.com/network`
(newdash), find the **Domains** section, click **Add Domain → Free
Subdomain**, and pick something like `yourname.dart.page`. Point the `A`
record at your VPS's floating / primary IP.

**Bring your own domain, use DartNode DNS.** In the dashboard **Domains**
section, click **Add Domain → Just Use My Domain** and enter
`example.com`. DartNode then tells you to delegate the zone by setting
these nameservers at your current registrar:

```
ns1.web.dartnode.net
ns2.web.dartnode.net
```

Once delegation propagates (up to 24 h), add an `A` record in the DartNode
DNS page pointing `share.example.com` → VPS IP.

**Register or transfer the domain to DartNode ($10/yr).** Same dashboard,
**Register a New Domain** or **Transfer a Domain**. Once the domain lands,
the **DNS Records** tab lets you add the `A` record directly.

For rDNS (reverse DNS) — not required for speakeasy, but nice to have —
DartNode's **Network Center → rDNS Records** page lets you set the PTR for
the VPS IP to `share.example.com`.

#### Using Cloudflare / Namecheap / any other DNS host

Log in to whichever service hosts your DNS zone and add an `A` record:

| Field | Value                             |
|-------|-----------------------------------|
| Type  | `A`                               |
| Name  | `share` (or `@` for the apex)     |
| Value | Your VPS's public IPv4            |
| TTL   | 300 seconds (raise later)         |

If you use Cloudflare, **turn the proxy (orange cloud) OFF** for this
record — speakeasy handles its own TLS with autocert, and an extra proxy
will either break the ACME challenge or put Cloudflare in the path of
every request. Leave it as "DNS only" (grey cloud).

#### Verify

```shell
# `dig` from dnsutils — install first on Ubuntu/Debian:
sudo apt install -y dnsutils
dig +short share.example.com

# Or, without installing anything (glibc has getent built in):
getent hosts share.example.com

# Or: a one-liner in Python 3 (present on almost every distro):
python3 -c "import socket; print(socket.gethostbyname('share.example.com'))"
```

Any of these should print the VPS's public IP.

### Open firewall ports

Speakeasy binds ports 80 and 443 on the VPS. Nothing else needs to be open
to the public.

```shell
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

---

## Step 2 — Tailscale tunnel

Install Tailscale on both machines. This is the "cable" between VPS and
home.

### On the VPS

```shell
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
# Click the URL it prints and authenticate.
```

Give the VPS a nice name in the [Tailscale
admin](https://login.tailscale.com/admin/machines) (e.g. `share-vps`).

### On the home box

```shell
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up
# Authenticate the same way.
```

Give it a name too (e.g. `home-server`).

### Verify the tunnel

On the VPS:

```shell
tailscale status
# You should see home-server in the list with a 100.x.x.x IP.

# Confirm the VPS can reach the home box's app port:
curl -s http://home-server:3000/ | head -5
# Or by IP: curl -s http://100.100.100.100:3000/
```

If that works, the tunnel is good. Note the hostname or IP — you'll wire it
into speakeasy as the upstream.

**Tip:** Tailscale's MagicDNS lets you use the hostname (`home-server:3000`)
instead of the 100.x IP. Leave MagicDNS enabled; it's easier to read.

### Lock down the home app

On the home box, make sure the app only accepts connections from the
Tailscale interface — not from your LAN, not from anywhere. Exactly how
depends on the app, but typically:

```shell
# Your app listens on 0.0.0.0:3000? Change it to bind only to the
# Tailscale address:
tailscale ip -4      # prints the home box's tailscale IP, e.g. 100.64.1.5
# Then start the app with: --bind 100.64.1.5:3000
# Or bind to 127.0.0.1:3000 plus a tailscale serve / funnel rule.
```

If the app only binds to `127.0.0.1`, Tailscale won't reach it. Bind to
`0.0.0.0` or to the Tailscale IP.

---

## Step 3 — Install speakeasy on the VPS

Until the `.deb` package ships, build from source. Go 1.25 is required.

```shell
# Install Go (adjust version if newer is out)
curl -LO https://go.dev/dl/go1.25.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.25.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
. /etc/profile.d/go.sh

# Build speakeasy
git clone https://git.hou.snaju.com/dpedersen/speakeasy.git
cd speakeasy
go build -o speakeasy ./cmd/speakeasy
sudo install -m 0755 speakeasy /usr/local/bin/
speakeasy --version
```

### Create the service user and directories

```shell
sudo useradd -r -s /usr/sbin/nologin -d /var/lib/speakeasy speakeasy
sudo mkdir -p /etc/speakeasy /var/lib/speakeasy
sudo chown -R speakeasy:speakeasy /var/lib/speakeasy
sudo chmod 0750 /var/lib/speakeasy
```

### Put yourself in the `speakeasy` group

So you can run the CLI (`speakeasy token mint ...`) without `sudo`:

```shell
sudo usermod -aG speakeasy $USER
# Log out + back in for group membership to take effect.
```

### Write `/etc/speakeasy/config.toml`

```shell
sudo tee /etc/speakeasy/config.toml >/dev/null <<'EOF'
[server]
domain    = "share.example.com"
site_name = "My Stuff"

[tls]
mode = "autocert"

# Bind a route to the app on your home box.
# 'upstream' is whatever the VPS reaches it at over the Tailscale tunnel.
[[routes]]
name     = "homeapp"
path     = "/homeapp"
upstream = "http://home-server:3000"
# strip_prefix = true   # uncomment if your home app serves from its root
EOF
sudo chown root:speakeasy /etc/speakeasy/config.toml
sudo chmod 0640 /etc/speakeasy/config.toml
```

Validate it:

```shell
sudo -u speakeasy speakeasy --config /etc/speakeasy/config.toml config validate
# Should print: OK: /etc/speakeasy/config.toml (1 route(s))
```

---

## Step 4 — systemd service

Speakeasy runs as the `speakeasy` user but needs to bind ports 80 and 443.
systemd handles that with `AmbientCapabilities`.

```shell
sudo tee /etc/systemd/system/speakeasy.service >/dev/null <<'EOF'
[Unit]
Description=Speakeasy Gateway
After=network-online.target tailscaled.service
Wants=network-online.target

[Service]
Type=simple
User=speakeasy
Group=speakeasy
ExecStart=/usr/local/bin/speakeasy --config /etc/speakeasy/config.toml serve
Restart=on-failure
RestartSec=5

# Bind 80/443 without running as root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

# The unix socket lives at /run/speakeasy/speakeasy.sock; systemd creates
# the directory with the right owner and wipes it at shutdown.
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
EOF

sudo systemctl daemon-reload
sudo systemctl enable --now speakeasy
sudo systemctl status speakeasy
```

Check the logs to make sure autocert picked up a cert (takes ~10s on first
start):

```shell
sudo journalctl -u speakeasy -n 30 --no-pager
# Look for: "gateway: autocert on :443 (+ACME/redirect on :80) for share.example.com"
```

### Smoke test the public side

```shell
curl -i https://share.example.com/health
# HTTP/2 200
# ok

curl -i https://share.example.com/denied
# HTTP/2 401
# (minimal "Access denied." page)
```

---

## Step 5 — Set an admin password (optional but recommended)

If you want the web admin panel at `https://share.example.com/admin`:

```shell
# Run as the speakeasy user so the hash file ends up with the right owner.
sudo -u speakeasy speakeasy --config /etc/speakeasy/config.toml \
  admin set-password
# New admin password: ****
# Confirm password:   ****
# Wrote hash to /var/lib/speakeasy/admin.hash (chmod 0600)

sudo systemctl restart speakeasy
```

Visit `https://share.example.com/admin` — Basic Auth prompts with username
`admin`, your chosen password.

Without this, the admin panel is disabled. The CLI over the unix socket
still works — that's how the filesystem-perms auth model kicks in.

---

## Step 6 — Mint your first invite

You have two ways: CLI on the VPS, or the web panel from your laptop.

### CLI

```shell
# You are in the `speakeasy` group at this point; no sudo needed.
speakeasy --config /etc/speakeasy/config.toml token mint \
  --label alice \
  --route homeapp \
  --expires 30d \
  --msg "Hey Alice, take a look at the dashboard."

# Token minted
#   Label:   alice
#   Route:   homeapp
#   Expires: 2026-05-19T...Z
#   JTI:     ...
#
# Share link:
#   https://share.example.com/invite?token=eyJ...
```

Copy the **Share link** and send it via whatever channel you like — email,
iMessage, Signal, a Slack DM. Treat it like a secret: anyone with the link
gets in until it expires or you revoke it.

### Admin panel

1. Open `https://share.example.com/admin`.
2. Fill the mint form (label, route dropdown, expires, optional message).
3. Click **Mint token**. The result card shows:
   - Click-to-copy invite URL
   - Click-to-copy raw token and JTI
   - QR code (handy for mobile recipients)

---

## Step 7 — Recipient's experience

Alice clicks the link:

1. **Landing page** at `https://share.example.com/invite?token=…`: brief
   "You've been invited to My Stuff" with your message and a 5-second
   countdown.
2. Her browser sets a session cookie (`speakeasy_session`, 24-hour default).
3. She's redirected to `/homeapp` — which speakeasy proxies through the
   Tailscale tunnel to `http://home-server:3000`.
4. From her side, it's just "a website." No account, no login.

If she hits any other URL (`/something-else`, `/admin`, `/`), she sees the
same minimal "Access denied" page — the site name and your routes are
deliberately not leaked.

---

## Step 8 — Revoke when done

```shell
speakeasy --config /etc/speakeasy/config.toml token revoke alice
# Revoked alice
```

Or click **Revoke** in the admin panel. Alice's session dies on her next
request (no 60-second denylist-reload window — speakeasy deletes the
session row directly when a token is revoked).

---

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| `curl https://share.example.com/health` hangs | DNS not yet pointing at VPS, or ports 80/443 not open in `ufw` |
| 502 Bad Gateway on `/homeapp` | VPS can't reach the home box. `tailscale status` on VPS; `curl http://home-server:3000/` from VPS |
| autocert fails with "unable to authorize" | Port 80 isn't reachable from the internet. Some clouds need explicit port-80 rules even when UFW is set |
| `/admin` returns 401 forever | Admin password not set, or service wasn't restarted after `set-password` |
| CLI says `permission denied … speakeasy.sock` | Your shell user isn't in the `speakeasy` group yet (log out + in after `usermod -aG`) |
| `/homeapp` works but assets 404 | Your app serves links relative to `/` but lives under `/homeapp`. Either configure the app's base-URL, or mount it at `path = "/"` and add `strip_prefix = true` |

---

## Hardening

**Restrict admin to your own IP.** Currently `/admin` is internet-reachable
(protected by basic auth). You can additionally refuse requests that didn't
come from your home Tailscale IP. For now, speakeasy itself doesn't have
an IP allowlist; put it behind Tailscale Serve or an upstream WAF if you
need that. (Planned for v2.)

**Lower the session TTL** for sensitive shares:

```toml
[server]
session_ttl = 3600   # 1 hour
```

**Rotate the signing key** to invalidate *all* outstanding tokens at once:

```shell
sudo systemctl stop speakeasy
sudo rm /var/lib/speakeasy/signing.key
sudo systemctl start speakeasy   # generates a fresh key
```

Every existing invite link becomes "not valid"; re-mint for anyone still
needs access.

---

## Appendix — alternative tunnels

Tailscale is the easiest path. If you prefer:

### Bare WireGuard

Run `wireguard-tools` on both machines; use a keypair per peer; configure
the VPS as the WG server (listens UDP 51820) and the home box as a peer.
Home→VPS is outbound UDP, so NAT/CGNAT isn't a problem for the home side.
In `/etc/speakeasy/config.toml`, set `upstream` to the home box's
WG address (e.g. `http://10.8.0.2:3000`).

Tradeoff vs. Tailscale: no CGNAT fallback via DERP relays, no admin UI,
manual key management. Win: no third-party service at all.

### SSH reverse tunnel

Quick-and-dirty for one-off tests:

```shell
# On the home box:
ssh -N -R 127.0.0.1:3000:localhost:3000 don@share.example.com
```

Now the VPS sees `localhost:3000` as the home app. In
`config.toml`: `upstream = "http://127.0.0.1:3000"`.

Downsides: single point of failure (the SSH process), no auto-reconnect
unless you wrap it with `autossh`, ssh daemon churn.

### Cloudflare Tunnel

Cloudflare Tunnel is a *different* shape — it replaces the VPS entirely
rather than linking home to a VPS. See `README.md` → "Home machine behind
a residential ISP" for that setup. Either shape works; the home↔VPS flow
documented here is useful when you want full control of the edge
(logging, IP allowlists, etc.).
