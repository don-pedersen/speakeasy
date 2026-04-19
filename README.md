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

### Quick start (build from source)

```shell
# Requires Go 1.25+
git clone https://git.hou.snaju.com/dpedersen/speakeasy.git
cd speakeasy
go build -o speakeasy ./cmd/speakeasy
sudo install -m 0755 speakeasy /usr/local/bin/
```

A `.deb` / `.rpm` install target (via `nfpm`) is the next step.

### Minimal config

Create `/etc/speakeasy/config.toml`:

```toml
[server]
domain = "yoursite.com"

[tls]
mode = "autocert"            # Let's Encrypt, needs ports 80+443

[[routes]]
name     = "preview"
path     = "/preview"
upstream = "http://localhost:8080"
```

Then:

```shell
# Set an admin password for the web panel (optional — CLI works without it)
sudo speakeasy admin set-password

# Run the gateway
sudo speakeasy serve
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

### VPS with public IP

Run speakeasy with `tls.mode = "autocert"`. Point DNS at the VPS, open ports
80+443, run `speakeasy serve` under systemd. Done.

### Home machine behind a residential ISP

Residential ISPs commonly block inbound 80/443, break Let's Encrypt via
dynamic IPs, or use CGNAT. Use an outbound tunnel instead:

**Cloudflare Tunnel** (easiest): run `cloudflared` on the home box → outbound
tunnel to CF → CF serves `yoursite.com` and forwards plain HTTP to speakeasy.
Configure:

```toml
[server]
domain      = "yoursite.com"
trust_proxy = true        # CF terminates TLS; trust X-Forwarded-* and keep Secure cookies

[tls]
mode = "http"             # CF handles TLS; speakeasy serves plain HTTP on LAN
```

**Alternatives**: boringproxy, frp, Tailscale Funnel, or a $5/mo VPS with a
WireGuard tunnel back to home (speakeasy on the VPS, app at home).

## Configuration reference

See [`packaging/config.toml.example`](packaging/config.toml.example) for the
full commented template. Key options:

| Key                              | Default                        | Purpose |
|----------------------------------|--------------------------------|---------|
| `server.domain`                  | (required)                     | Public hostname. |
| `server.site_name`               | `server.domain`                | Shown on invite landing. |
| `server.trust_proxy`             | `false`                        | Keep `Secure` cookies behind a TLS-terminating proxy. |
| `server.data_dir`                | `/var/lib/speakeasy`           | SQLite DB, autocert cache, signing key. |
| `server.socket_path`             | `/run/speakeasy/speakeasy.sock`| CLI↔daemon IPC. Group `speakeasy`, mode 0660. |
| `server.admin_password_file`     | `<data_dir>/admin.hash`        | Bcrypt hash for `/admin`. Missing = panel disabled. |
| `server.session_ttl`             | `86400` (24h)                  | Session cookie lifetime. |
| `tls.mode`                       | `autocert`                     | `autocert` / `files` / `http`. |
| `[[routes]]`                     | —                              | One per gated mount path. |

## Architecture

```
cmd/speakeasy/          # cobra CLI (serve, token, config, admin)
internal/config/        # TOML loader + validation
internal/store/         # SQLite-backed tokens & sessions (pure-Go sqlite)
internal/token/         # JWT HS256 signer + key generation
internal/admin/         # HTTP API (token CRUD) + basic-auth + web UI
internal/gateway/       # TLS, route matching, session middleware, proxy
internal/daemon/        # process wiring (DB + socket + gateway + signals)
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

Remaining for v1:

- [ ] `.deb` / `.rpm` packaging via `nfpm` + systemd unit

Beyond v1: host-based routing, arbitrary path scopes, multi-route tokens,
Ed25519 signing, token families, magic-link email delivery.

## License

(TODO — pick a license.)
