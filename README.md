# Speakeasy

*You need the password to get in.*

A lightweight, self-hosted private web publishing gateway. Speakeasy sits in
front of any web application and restricts access to invited users via signed,
short-lived JWT links. Share a URL, not a password-manager entry. Works with
any upstream stack.

---

## Why

You have something running on a box — a prototype, a dashboard, a client
preview, a Jellyfin server, an internal dashboard — and you want to share it
with specific people without making it public. Speakeasy gives each recipient
their own invite link. No OAuth, no user accounts, no SSO config. Just a
signed token, a mount path, and a short-lived session cookie.

- **Per-person access** — every token is bound to exactly one named route; a
  token for "alice" reaches only what Alice is meant to see.
- **Link-based onboarding** — recipients click a URL and are in; no signup.
- **Revoke instantly** — one command kills the token and its live sessions.
- **Self-hosted** — single static binary, SQLite, no runtime dependencies.

## How it works

```
         Recipient's browser
                 │
                 ▼
 https://yoursite.com/invite?token=<JWT>
                 │
                 ▼
  ┌────────────────────────────┐
  │      speakeasy (Go)        │──┐
  │  TLS • route matching •    │  │  admin panel (/admin)
  │  session cookies • proxy   │  │  HTTP Basic Auth
  └────────────────────────────┘  │
         │                        │
         ▼                        ▼
  ┌──────────────┐       ┌──────────────────┐
  │ your upstream│       │ speakeasy CLI    │
  │ app (:8080)  │       │ (unix socket,    │
  └──────────────┘       │  no password)    │
                         └──────────────────┘
```

1. You mint a token for "alice", bound to route `client-preview`.
2. Alice clicks `https://yoursite.com/invite?token=…`, gets a polished landing
   page with a 5-second countdown and a session cookie.
3. Her cookie only grants access to paths under the `client-preview` mount.
4. You can revoke her token at any time; her session dies on her next request.

## Install

### One-line install on a Debian/Ubuntu VPS

```shell
curl -fsSL https://git.hou.snaju.com/dpedersen/speakeasy/raw/main/scripts/install.sh | sudo bash
```

That installs Go (if missing), clones + builds speakeasy, creates the
`speakeasy` system user, writes a hardened systemd unit, adds you to the
`speakeasy` group, and drops a commented example config. It stops short of
starting the daemon — generate your config with `init`, then launch:

```shell
# Generate the config (interactive prompts; or pass all --flags for unattended)
sudo speakeasy init

# (optional) enable the /admin web panel
sudo -u speakeasy speakeasy admin set-password

# Launch
sudo systemctl enable --now speakeasy
```

Log out + back in once so your shell picks up the `speakeasy` group, then
you can run `speakeasy token mint …` without `sudo`.

For fully scripted provisioning:

```shell
sudo speakeasy init \
  --domain example.com --route-name app \
  --route-path / --route-upstream http://localhost:8080
sudo systemctl enable --now speakeasy
```

### Build from source manually

```shell
# Requires Go 1.25+
git clone https://github.com/don-pedersen/speakeasy.git
cd speakeasy
go build -o speakeasy ./cmd/speakeasy
sudo install -m 0755 speakeasy /usr/local/bin/
```

Then create `/etc/speakeasy/`, the `speakeasy` user, the systemd unit — or
just run `./scripts/install.sh` from a clone, it's the same script.

A `.deb` / `.rpm` install path (via `nfpm`) is the next planned step.

### Minimal config

```toml
# /etc/speakeasy/config.toml
[server]
domain = "yoursite.com"

[tls]
mode = "autocert"              # Let's Encrypt, needs ports 80+443

[[routes]]
name     = "preview"
path     = "/preview"
upstream = "http://localhost:8080"
```

## Usage

### Mint a token — CLI

```shell
speakeasy token mint --label alice --route preview --expires 30d \
  --msg "Here's the new prototype, take a look!"
```

Output includes a ready-to-share invite URL. Copy and send it.

### Mint a token — web admin panel

Navigate to `https://yoursite.com/admin`, enter the admin password, fill the
mint form. The response shows the invite URL, raw token, JTI, and a QR code
suitable for tap-to-share on mobile.

### Revoke

```shell
speakeasy token revoke alice
```

Or click **Revoke** in the admin panel. Alice's live sessions die
immediately — no denylist reload window.

### List

```shell
speakeasy token list
```

## Deployment topologies

### 1) VPS with public IP — simplest

Run speakeasy with `tls.mode = "autocert"` and you're done. Point DNS at the
VPS, open ports 80+443, `scripts/install.sh` handles the rest. This is the
shape the one-line installer targets.

### 2) Home app behind a VPS tunnel — recommended for sharing home machines

Residential ISPs often block inbound 80/443, break Let's Encrypt via dynamic
IPs, or use CGNAT. Solution: run speakeasy on a cheap public VPS, connect the
home box to it over an overlay network, proxy invitees to the home app.

Three substrates for the tunnel, in increasing order of self-containment:

- **Tailscale (free tier)** — 3 users / 100 devices, easy, 5 minutes. Uses
  Tailscale's coordinator/DERP infra. Fine for personal projects.
- **Headscale (self-hosted Tailscale coordinator) + Caddy on the VPS** — keeps
  the Tailscale client UX but the coordinator runs on your VPS. No cloud
  dependency for coordination; still uses Tailscale's public DERP relays
  unless you run your own. Caddy fronts both speakeasy and Headscale on 443
  with separate subdomains.
- **Bare WireGuard** — fewest moving parts, keys exchanged manually,
  long-term forgettable. No auto discovery, no MagicDNS.

Full walkthrough with all three, plus DartNode-specific DNS steps, Vite-app
gotchas, and SPA sub-path config:
**[docs/deploy-home-through-vps.md](docs/deploy-home-through-vps.md)**.

### 3) Home app behind Cloudflare Tunnel (no VPS needed)

Run `cloudflared` on the home box, outbound tunnel to Cloudflare, CF serves
`yoursite.com` and forwards plain HTTP to speakeasy on the home box:

```toml
[server]
domain      = "yoursite.com"
trust_proxy = true     # CF terminates TLS; trust X-Forwarded-* + keep Secure cookies

[tls]
mode = "http"          # CF handles TLS; speakeasy serves plain HTTP internally
```

Good for "I don't want to rent a VPS" scenarios. Downside: Cloudflare is in
the request path.

## Configuration reference

See [`packaging/config.toml.example`](packaging/config.toml.example) for the
full commented template. Key options:

| Key                              | Default                         | Purpose |
|----------------------------------|---------------------------------|---------|
| `server.domain`                  | (required)                      | Public hostname. |
| `server.site_name`               | `server.domain`                 | Shown on invite landing. |
| `server.trust_proxy`             | `false`                         | Keep `Secure` cookies behind a TLS-terminating proxy. |
| `server.data_dir`                | `/var/lib/speakeasy`            | SQLite DB, autocert cache, signing key. |
| `server.socket_path`             | `/run/speakeasy/speakeasy.sock` | CLI↔daemon IPC. Group `speakeasy`, mode 0660. |
| `server.admin_password_file`     | `<data_dir>/admin.hash`         | Bcrypt hash for `/admin`. Missing = panel disabled. |
| `server.session_ttl`             | `86400` (24h)                   | Session cookie lifetime. |
| `tls.mode`                       | `autocert`                      | `autocert` / `files` / `http`. |
| `[[routes]]`                     | —                               | One per gated mount path. |

## Architecture

```
cmd/speakeasy/          # cobra CLI (serve, init, token, config, admin)
internal/config/        # TOML loader + validation
internal/store/         # SQLite-backed tokens & sessions (pure-Go sqlite)
internal/token/         # JWT HS256 signer + key generation
internal/admin/         # HTTP API (token CRUD) + basic-auth + web UI
internal/gateway/       # TLS, route matching, session middleware, proxy
internal/daemon/        # process wiring (DB + socket + gateway + signals)
scripts/install.sh      # one-line VPS installer
packaging/              # example config + (soon) nfpm specs
docs/                   # deployment guides
```

### Design notes

- **Sessions are DB-backed, not JWT.** The cookie is an opaque random ID
  pointing at a row in SQLite. Revocation is immediate (delete the row); no
  denylist-reload window as with JWT sessions.
- **`jti` is the stable token identity, not `label`.** A partial unique index
  enforces "one active token per label" while keeping revoked rows as history.
- **Two paths into the admin API.** The same handlers are served on a unix
  socket (CLI, filesystem-perm auth) and on HTTPS (`/admin`, HTTP Basic Auth).
  The CLI never writes the DB directly.
- **Named routes, not path scopes.** Each token binds to exactly one route;
  the gateway enforces the path prefix. One-token-per-route in v1; scope
  arrays can come later.
- **Go stack, static binary.** `modernc.org/sqlite` (no CGO), `autocert`,
  `golang-jwt`, `skip2/go-qrcode`, `BurntSushi/toml` — all pure Go.

## Security

- All access tokens are HS256 JWTs signed with a key stored at
  `<data_dir>/signing.key` (auto-generated on first run, mode 0600).
- Session cookies are `HttpOnly; SameSite=Strict; Secure` (unless
  `tls.mode = "http"` and `trust_proxy = false` — that is a dev-only combo).
- The admin panel password is stored as a bcrypt hash (cost 12) with
  constant-time comparison.
- `/denied` intentionally leaks no site information — no site name, no route
  names, no counts. The gated-path-without-session path uses the same
  response.
- Upstream requests have the speakeasy session cookie stripped; other cookies
  pass through.

## Project status

v1 in active development. Working:

- [x] Gateway, TLS (autocert / files / http), route matching, proxy
- [x] Session management, invite flow, polished landing + denied pages
- [x] Admin CLI (`token mint/list/revoke/unrevoke`) over unix socket
- [x] Admin web panel with QR codes, basic auth
- [x] Reserved-path validation, graceful shutdown, embedded templates
- [x] One-line VPS installer (`scripts/install.sh`) + `speakeasy init` config generator
- [x] Home-via-VPS deployment guide (Tailscale / Headscale / WireGuard)

Remaining for v1:

- [ ] `.deb` / `.rpm` packaging via `nfpm`
- [ ] Docker image (maybe)

Beyond v1: host-based routing, arbitrary path scopes, multi-route tokens,
Ed25519 signing, token families, magic-link email delivery, self-hosted DERP
relay for the Headscale topology.

## License

(TODO — pick a license.)
