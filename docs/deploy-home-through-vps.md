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

### Reaching apps on *other* LAN machines

If the app you want to share runs on a different box than the Tailscale
peer (say, `192.168.86.51:5173` while Tailscale is installed on
`home-server`), the VPS can't see it directly — Tailscale only connects
hosts that run the daemon. You have three choices, in order of how I'd
actually pick:

**(a) Turn `home-server` into a subnet router.** One Tailscale install
exposes the whole LAN; new apps = new `[[routes]]` blocks, no tunnel
churn.

```shell
# On home-server — enable IP forwarding once:
echo 'net.ipv4.ip_forward=1'         | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding=1'| sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf

# Advertise your LAN to the tailnet (adjust subnet):
sudo tailscale up --advertise-routes=192.168.86.0/24
```

Then in the [Tailscale admin](https://login.tailscale.com/admin/machines),
click `home-server` → **Edit route settings** → approve
`192.168.86.0/24`.

On the VPS, accept the advertised routes:

```shell
sudo tailscale up --accept-routes
curl -sI http://192.168.86.51:5173/   # LAN IP now reachable from the VPS
```

Your speakeasy upstream becomes `http://192.168.86.51:5173` — the LAN
address directly.

**(b) Install Tailscale on each app host.** Simple when it's one or two
machines. Gets tedious for many devices, impossible for things that
can't run the daemon (printers, IoT, appliances).

**(c) Reverse-proxy hop on `home-server`.** Run nginx/caddy on the
Tailscale host that forwards to the LAN app. Works without subnet
routing but adds a hop for no real gain; use only if (a) and (b) aren't
an option.

### Lock down the home app

Once the VPS can reach your app, make sure the app *itself* is bound
sanely. Exactly how depends on the app, but two cases are very common:

**Production apps** (a built binary, a static site, a running service):
bind to the interface that speakeasy reaches it on, and nothing else.

```shell
# If the app lives on the Tailscale peer itself:
tailscale ip -4        # prints 100.x.x.x for this host
# Start the app bound to that address — not 0.0.0.0, not 127.0.0.1.

# If the app lives on a LAN host reached via subnet route:
# Bind it to the LAN IP (192.168.86.51), or to 0.0.0.0 and firewall the LAN.
```

If the app only binds to `127.0.0.1`, Tailscale / the LAN won't reach
it. You'll see `connection refused` from the VPS.

**Vite / Next.js / Rails / similar dev servers** have extra quirks
because they were designed for same-machine browser access:

- **Bind**: default is `127.0.0.1`. Start Vite with `--host` (or
  `server.host: true` in `vite.config.*`); Next with `next dev -H 0.0.0.0`;
  Rails with `bin/rails s -b 0.0.0.0`.
- **Host header allowlist** (Vite 5+): requests from `donpedersen.com`
  are rejected by default. Add your domain:

  ```js
  // vite.config.js
  export default {
    server: {
      host: true,
      allowedHosts: ['donpedersen.com', '.donpedersen.com'],
    },
  }
  ```

  Next.js and Rails have similar knobs; consult each project's docs if
  you see a "Not Found" or "Blocked host" error with a working
  speakeasy route.
- **HMR / live-reload**: Vite's hot-reload uses a WebSocket back to the
  origin. Speakeasy's reverse proxy handles WS upgrades, but if the
  browser console shows HMR connecting to `ws://localhost:5173`, set
  `server.hmr.clientPort: 443` and `server.hmr.protocol: 'wss'` in the
  Vite config.

For a production deployment, ignore all of this — dev-server gotchas
only matter when you're sharing a dev server (which is great for
showing work-in-progress to a client, slightly more fiddly than
shipping a static build).

---

## Step 3 — Install speakeasy on the VPS

Until the `.deb` package ships, build from source. Go 1.25+ is required.

Don't use `apt install golang-go` — Debian/Ubuntu ships an older toolchain
that won't satisfy speakeasy's transitive deps. Install the upstream tarball:

```shell
# Grab whatever Go calls "current stable" today (e.g. go1.26.2).
GO_VER=$(curl -fsSL https://go.dev/VERSION?m=text | head -1)
curl -LO "https://go.dev/dl/${GO_VER}.linux-amd64.tar.gz"

# Sanity-check before extracting — a failed download would otherwise leave
# you with a saved HTML error page and a cryptic tar error.
file "${GO_VER}.linux-amd64.tar.gz"
# Expected: <file>: gzip compressed data, ...

sudo tar -C /usr/local -xzf "${GO_VER}.linux-amd64.tar.gz"
echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee /etc/profile.d/go.sh
. /etc/profile.d/go.sh
go version

# Build speakeasy
git clone https://github.com/don-pedersen/speakeasy.git
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
# Make sure $USER is actually your login name first — it won't be if
# you su'd into root, or if you sudo this command with a fresh env.
echo "$USER"
sudo usermod -aG speakeasy "$USER"
```

**Then log out and back in** — group membership is loaded at session
creation, so an existing shell won't see it until you reconnect. Verify:

```shell
groups | tr ' ' '\n' | grep -qx speakeasy && echo "OK — in speakeasy group" \
  || echo "NOT in speakeasy group yet; re-log in (or run 'newgrp speakeasy')"

# And as a real-world check — this should work without sudo once the
# daemon is running (next steps). "(no tokens)" is the expected output.
# speakeasy --config /etc/speakeasy/config.toml token list
```

If you skip this, every CLI call will need `sudo` — functional, but less
tidy. (The daemon itself doesn't care; it's the `/run/speakeasy/speakeasy.sock`
file that's group-readable.)

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
