# Speakeasy

*You need the password to get in.*

A lightweight, self-hosted private web publishing gateway. Speakeasy sits in front of any web application on your VPS and restricts access to invited users via signed JWT tokens. Share a link, not a password manager entry. Works with any stack.

---

## Table of Contents

1. [Concept](#concept)  
2. [Architecture](#architecture)  
3. [Directory Structure](#directory-structure)  
4. [Components](#components)  
5. [Token Design](#token-design)  
6. [Configuration](#configuration)  
7. [API Endpoints](#api-endpoints)  
8. [Token Management CLI](#token-management-cli)  
9. [Admin Panel](#admin-panel)  
10. [Invitation Landing Page](#invitation-landing-page)  
11. [Sharing Flow — End to End](#sharing-flow--end-to-end)  
12. [Nginx / Caddy Integration](#nginx--caddy-integration)  
13. [Deployment Steps](#deployment-steps)  
14. [Security Considerations](#security-considerations)  
15. [Future Enhancements](#future-enhancements)

---

## Concept

You have a website running on a VPS that you want to share with specific people — clients, collaborators, testers — without making it fully public. Speakeasy wraps your site with a JWT gate:

- You mint a signed token for each person (or a shared token for a group)  
- You send them a link: `https://yoursite.com/enter?token=<JWT>`  
- They click it once, get a session cookie, and browse normally  
- Anyone without a valid token sees nothing

No OAuth. No user accounts. No database required. Just a shared secret, signed tokens, and a small middleware process.

---

## Architecture

```
Internet
    │
    ▼
[ Caddy / Nginx ]   ← TLS termination, reverse proxy
    │
    ▼
[ Speakeasy Gateway ]   ← Node.js auth middleware (port 3000)
    │  - Validates JWT on /enter
    │  - Issues session cookie
    │  - Checks cookie on all other routes via auth_request
    │
    ▼
[ Your Web App ]   ← Any stack (port 8080, or static files)
```

### Request flow

1. User visits `https://yoursite.com/enter?token=<JWT>`  
2. Speakeasy validates the JWT signature and expiry  
3. If valid, sets an `HttpOnly` session cookie (`speakeasy_session`) and redirects to `/`  
4. Caddy/Nginx uses `auth_request` to hit Speakeasy's `/auth/verify` on every subsequent request  
5. Speakeasy checks the session cookie → 200 OK (pass through) or 401 (redirect to deny page)  
6. Denied users see a minimal "Access Denied" page with no information leakage

---

## Directory Structure

```
speakeasy/
├── src/
│   ├── server.js           # Express app — main gateway process
│   ├── auth.js             # JWT validation + session logic
│   ├── middleware.js        # Express middleware: verify session cookie
│   ├── registry.js         # Token registry read/write (config/tokens.json)
│   ├── routes/
│   │   ├── enter.js        # GET /enter?token=... — validate & set cookie (raw entry)
│   │   ├── invite.js       # GET /invite?token=... — friendly invitation landing page
│   │   ├── verify.js       # GET /auth/verify — used by nginx auth_request
│   │   ├── deny.js         # GET /denied — access denied page
│   │   └── admin.js        # GET /admin + POST /admin/api/* — admin panel & API
│   └── views/
│       ├── denied.html     # Minimal access denied page
│       ├── invite.html     # Invitation landing page (countdown + welcome msg)
│       └── admin.html      # Admin panel UI (mint, list, revoke)
├── cli/
│   └── mint.js             # CLI tool to generate and revoke tokens
├── config/
│   ├── speakeasy.config.js # Loaded at runtime
│   ├── tokens.json         # Token registry (created on first mint)
│   └── revoked.json        # Revocation denylist (created on first revoke)
├── .env                    # Secret key + runtime settings (never commit)
├── .env.example            # Template for .env
├── package.json
├── caddy/
│   └── Caddyfile           # Ready-to-use Caddy config
├── nginx/
│   └── speakeasy.conf      # Ready-to-use Nginx site config
└── systemd/
    └── speakeasy.service   # systemd unit file for auto-start
```

---

## Components

### 1\. `src/server.js` — Gateway Process

An Express.js application running on a configurable port (default `3000`). It does not serve your app's content — it only handles auth logic. Your actual app runs separately on its own port.

**Responsibilities:**

- Expose `/enter`, `/auth/verify`, and `/denied` routes  
- Read config from `.env` at startup  
- Log all access attempts with timestamps and token identity (if decoded)

### 2\. `src/auth.js` — JWT & Session Logic

Core logic module. Handles:

- **Token validation**: Verify JWT signature using `SPEAKEASY_SECRET` from env. Reject expired tokens. Reject tokens with unknown `aud` (audience) if audience checking is enabled.  
- **Session issuance**: On valid token, sign a short-lived session JWT (separate from the access token) and set it as an `HttpOnly`, `Secure`, `SameSite=Strict` cookie named `speakeasy_session`.  
- **Session verification**: On each `auth_request`, decode and verify the session cookie. Return `200` or `401`.

### 3\. `cli/mint.js` — Token Minter

A command-line tool for creating new access tokens.

```shell
# Mint a token that never expires
node cli/mint.js --label "alice"

# Mint a token expiring in 7 days
node cli/mint.js --label "bob" --expires 7d

# Mint a token expiring at a specific date
node cli/mint.js --label "client-demo" --expires 2025-12-31

# Output a ready-to-share URL
node cli/mint.js --label "alice" --expires 30d --url https://yoursite.com
```

Output:

```
Token minted for: alice
Expires: 2025-05-19T00:00:00.000Z

Token:
eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...

Share this link:
https://yoursite.com/enter?token=eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...
```

### 4\. `src/registry.js` — Token Registry

Handles reading and writing `config/tokens.json`. Exposes functions used by both the CLI and the admin API:

- `mintToken(label, expires, msg)` — signs a JWT, appends to registry, returns token string  
- `revokeLabel(label)` — adds label to `config/revoked.json`, sets `revoked: true` in registry  
- `updateLastUsed(label)` — called by the session verifier on each authenticated request  
- `listTokens()` — returns the full registry array  
- `isRevoked(label)` — checks the in-memory denylist (reloaded on interval)

### 5\. `src/views/admin.html` — Admin Panel

A self-contained single-page UI. No build step required. Communicates with the admin API via `fetch()`. Includes:

- Token mint form with label, expiry dropdown, and optional message field  
- Results area showing token, copyable invite URL, and inline QR code (generated client-side using the `qrcode` npm package served as a script tag)  
- Token table with live status (active / expired / revoked) and per-row revoke button  
- Revocation list with unrevoke option

### 6\. `src/views/invite.html` — Invitation Landing Page

A polished single-page entry experience for recipients. Shows a welcome message if one was encoded in the token, counts down 5 seconds, then redirects. Served by `/invite?token=<JWT>`. Token validation happens server-side before the page is rendered — the page is only shown if the token is valid. Invalid tokens render an error state of the same page.

### 7\. `src/views/denied.html` — Access Denied Page

A minimal, intentionally sparse HTML page shown to unauthenticated users. No branding, no information about what the site is or who runs it. Just a clean "Access denied." message. This prevents information leakage about the site's purpose.

---

## Token Design

Speakeasy uses two token types:

### Access Token (you mint and share)

```json
{
  "sub": "alice",
  "iss": "speakeasy",
  "iat": 1713000000,
  "exp": 1715592000,
  "label": "alice",
  "v": 1
}
```

- Signed with `SPEAKEASY_SECRET` (HS256)  
- `sub` / `label`: human-readable identifier (for your logs)  
- `exp`: optional expiry (omit for non-expiring tokens)  
- `v`: token schema version (for future revocation list support)

### Session Token (issued by Speakeasy after access token is validated)

```json
{
  "sub": "alice",
  "iss": "speakeasy-session",
  "iat": 1713000000,
  "exp": 1713086400
}
```

- Signed with `SPEAKEASY_SESSION_SECRET` (separate key)  
- Short-lived (configurable, default 24 hours)  
- Stored as `HttpOnly` cookie — never exposed to JavaScript  
- Refreshed on each request if within the refresh window (configurable)

### Token Revocation

Speakeasy supports a simple flat-file denylist at `config/revoked.json`:

```json
["alice", "old-client-demo"]
```

When a session token is verified, the `sub` is checked against this list. Add a label to revoke all sessions for that person without rotating the master secret. The gateway reloads this file on a configurable interval (default: 60 seconds) — no restart required.

---

## Configuration

### `.env`

```
# Required
SPEAKEASY_SECRET=your-very-long-random-secret-here

# Optional — defaults shown
SPEAKEASY_SESSION_SECRET=another-long-random-secret
SPEAKEASY_ADMIN_PASSWORD=changeme                  # Password for /admin Basic Auth
SPEAKEASY_PORT=3000
SPEAKEASY_SESSION_TTL=86400          # Session cookie lifetime in seconds (24h)
SPEAKEASY_SESSION_REFRESH=3600       # Refresh session if >1h old (0 to disable)
SPEAKEASY_REVOKE_RELOAD=60           # Seconds between denylist reloads
SPEAKEASY_LOG_LEVEL=info             # info | debug | silent
SPEAKEASY_DENY_REDIRECT=             # Optional URL to redirect denied users to
SPEAKEASY_SITE_NAME=My Site          # Display name shown on the invitation page
SPEAKEASY_SITE_URL=https://yoursite.com  # Base URL used when minting share links
```

Generate secrets with:

```shell
node -e "console.log(require('crypto').randomBytes(48).toString('hex'))"
```

---

## API Endpoints

| Method | Path | Description |
| :---- | :---- | :---- |
| `GET` | `/invite?token=<jwt>` | Friendly invitation landing page — validates token, shows welcome, redirects |
| `GET` | `/enter?token=<jwt>` | Raw entry — validates token, sets session cookie, immediate redirect to `/` |
| `GET` | `/auth/verify` | Internal endpoint for Nginx/Caddy `auth_request`. Returns `200` or `401`. |
| `GET` | `/denied` | Human-facing access denied page |
| `GET` | `/health` | Returns `200 OK` — for uptime monitoring, no auth required |
| `GET` | `/admin` | Admin panel UI (Basic Auth required) |
| `POST` | `/admin/api/mint` | Mint a token. Body: `{ label, expires, msg }`. Returns token \+ invite URL. |
| `POST` | `/admin/api/revoke` | Revoke a label. Body: `{ label }`. |
| `POST` | `/admin/api/unrevoke` | Unrevoke a label. Body: `{ label }`. |
| `GET` | `/admin/api/tokens` | Return full token registry as JSON. |

---

## Token Management CLI

All token operations use `cli/mint.js`. The minting script reads `SPEAKEASY_SECRET` from `.env` automatically.

```shell
# Install deps first
npm install

# Mint tokens
node cli/mint.js --label "alice" --expires 30d
node cli/mint.js --label "bob"                    # no expiry
node cli/mint.js --label "demo" --expires 2025-12-31 --url https://mysite.com

# Revoke a label (adds to config/revoked.json)
node cli/mint.js --revoke "alice"

# List current revocations
node cli/mint.js --list-revoked
```

---

## Admin Panel

The admin panel is a simple, password-protected web UI served by Speakeasy itself. It lives at `/admin` and lets you mint tokens, copy share links, generate QR codes, and manage revocations — all from a browser, no SSH required.

### Access

The admin panel is protected by a separate `SPEAKEASY_ADMIN_PASSWORD` in `.env`. It is not JWT-gated — it uses HTTP Basic Auth so you can reach it even before any tokens exist. Caddy/Nginx can additionally restrict `/admin` to your IP address for extra safety (see config examples below).

### Admin Panel UI

The panel is a single self-contained HTML page served by Express. No build step, no framework. Just HTML \+ vanilla JS \+ minimal CSS inlined in the template.

```
src/views/admin.html
```

**Sections:**

**Mint a Token**

| Field | Description |
| :---- | :---- |
| Label | Name/identifier for this person (e.g. "alice", "client-acme") |
| Expires | Dropdown: 24 hours / 7 days / 30 days / 90 days / Never |
| Custom expiry | Optional date picker override |

On submit, the panel calls `POST /admin/api/mint` and displays:

- The raw token (monospace, click to copy)  
- A ready-to-share invitation URL (click to copy)  
- A QR code of the invitation URL (tap-to-share on mobile)

**Active Tokens**

A table of all minted tokens (read from `config/tokens.json` — see below), showing label, created date, expiry, and last-used timestamp. Each row has a **Revoke** button that calls `POST /admin/api/revoke` and updates `config/revoked.json` immediately.

**Revoked Labels**

A simple list of currently revoked labels with an **Unrevoke** option.

### Token Registry

To support the admin panel's token list, Speakeasy maintains a lightweight registry file:

```json
// config/tokens.json
[
  {
    "label": "alice",
    "created": "2025-04-19T12:00:00.000Z",
    "expires": "2025-07-18T12:00:00.000Z",
    "lastUsed": "2025-04-20T08:34:11.000Z",
    "revoked": false
  },
  {
    "label": "bob",
    "created": "2025-04-19T12:05:00.000Z",
    "expires": null,
    "lastUsed": null,
    "revoked": false
  }
]
```

This file is written by both the CLI and the admin panel when tokens are minted. The `lastUsed` field is updated by the gateway on each successful session verification. No database needed — the file is small and append-only in normal operation.

### Admin API Endpoints

| Method | Path | Description |
| :---- | :---- | :---- |
| `GET` | `/admin` | Serve the admin panel HTML |
| `POST` | `/admin/api/mint` | Mint a new token. Body: `{ label, expires }`. Returns token \+ invite URL. |
| `POST` | `/admin/api/revoke` | Revoke a label. Body: `{ label }`. |
| `POST` | `/admin/api/unrevoke` | Remove a label from the denylist. Body: `{ label }`. |
| `GET` | `/admin/api/tokens` | Return the token registry as JSON. |

All admin API routes require Basic Auth (`Authorization: Basic <base64(admin:<password>)>`). The browser handles this automatically once you log in to `/admin`.

### Restricting Admin to Your IP (Recommended)

**Caddy:**

```
handle /admin* {
    @blocked not remote_ip 203.0.113.42
    respond @blocked 403
    reverse_proxy localhost:3000
}
```

**Nginx:**

```
location /admin {
    allow 203.0.113.42;
    deny all;
    proxy_pass http://127.0.0.1:3000;
}
```

Replace `203.0.113.42` with your home/office IP. This means even a compromised `SPEAKEASY_ADMIN_PASSWORD` cannot be used from the outside world.

---

## Invitation Landing Page

Instead of dropping users directly into your app on first entry, Speakeasy shows a brief, branded **invitation page** at `/invite?token=<JWT>`. This is more polished than a raw `/enter` redirect and gives the recipient context before they land.

### Flow

```
User receives link:
https://yoursite.com/invite?token=<JWT>
         │
         ▼
  Speakeasy validates token
         │
    ┌────┴────┐
  Valid      Invalid / Expired
    │              │
    ▼              ▼
Invitation     Error page:
landing page   "This invite link is invalid
(5 seconds)     or has expired."
    │
    ▼
Auto-redirect to / with session cookie set
(or user clicks "Enter →" to go immediately)
```

### Invitation Page Design

A clean, minimal full-page layout (`src/views/invite.html`) showing:

- A subtle lock icon (SVG, inline — no external assets)  
- "You've been invited" heading  
- The site's configurable display name (`SPEAKEASY_SITE_NAME` in `.env`)  
- An optional welcome message (encoded into the JWT payload as `msg`)  
- A countdown ("Entering in 5…") with auto-redirect  
- An "Enter now →" button for impatient users

Example token with welcome message:

```shell
node cli/mint.js --label "alice" --expires 30d \
  --msg "Hey Alice, here's the prototype. Let me know what you think." \
  --url https://yoursite.com
```

The `msg` field is encoded in the JWT, so it travels with the link — no server-side storage needed.

### Error States on the Invitation Page

| Condition | Message shown |
| :---- | :---- |
| Token missing | "No invite token was provided." |
| Invalid signature | "This invite link is not valid." |
| Token expired | "This invite link has expired." |
| Label revoked | "This invite link has been revoked." |

All error states show the same minimal page style — no stack traces, no internal details.

---

## Sharing Flow — End to End

This is what the complete experience looks like for you and a recipient.

### You (on the VPS or in the admin panel)

**Option A — Admin Panel (recommended)**

1. Open `https://yoursite.com/admin` in your browser  
2. Log in with your admin password  
3. Fill in Label: `alice`, Expires: `30 days`  
4. Click **Mint Token**  
5. Panel shows the invitation URL and a QR code  
6. Click **Copy Link** and paste it into an email, Slack, iMessage, etc.

**Option B — CLI**

```shell
node cli/mint.js --label "alice" --expires 30d \
  --msg "Here's the new prototype, take a look!" \
  --url https://yoursite.com
```

Copy the printed link and send it.

### Alice (the recipient)

1. Receives a link: `https://yoursite.com/invite?token=eyJ...`  
2. Clicks it — sees the Speakeasy invitation page: *"You've been invited to \[Site Name\]"* with the optional message  
3. Waits 5 seconds (or clicks Enter now) — Speakeasy sets her session cookie and redirects her to `/`  
4. She browses the site normally. The cookie lasts 24 hours (configurable)  
5. After 24 hours, she'll need to click her link again — or you can extend her session TTL

### Revoking Alice's Access

**Admin Panel:** Click **Revoke** next to Alice's row in the token table.

**CLI:** `node cli/mint.js --revoke alice`

Within 60 seconds, Alice's next request will return a 401 and she'll see the access denied page.

---

## Nginx / Caddy Integration

### Caddy (`caddy/Caddyfile`)

```
yoursite.com {
    # Pass /enter, /auth/verify, /denied, /health directly to Speakeasy
    handle /enter* {
        reverse_proxy localhost:3000
    }
    handle /auth/* {
        reverse_proxy localhost:3000
    }
    handle /denied* {
        reverse_proxy localhost:3000
    }
    handle /health {
        reverse_proxy localhost:3000
    }

    # All other routes: verify auth first, then proxy to your app
    handle {
        forward_auth localhost:3000 {
            uri /auth/verify
            copy_headers X-Speakeasy-User
        }
        reverse_proxy localhost:8080
    }
}
```

### Nginx (`nginx/speakeasy.conf`)

```
server {
    listen 443 ssl;
    server_name yoursite.com;

    # SSL config (use certbot or your preferred method)
    ssl_certificate     /etc/letsencrypt/live/yoursite.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/yoursite.com/privkey.pem;

    # Speakeasy auth routes — proxy directly
    location ~ ^/(enter|denied|health) {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
    }
    location /auth/ {
        proxy_pass http://127.0.0.1:3000;
        proxy_set_header Host $host;
    }

    # Internal auth check endpoint
    location = /_speakeasy_verify {
        internal;
        proxy_pass http://127.0.0.1:3000/auth/verify;
        proxy_pass_request_body off;
        proxy_set_header Content-Length "";
        proxy_set_header X-Original-URI $request_uri;
    }

    # Your app — all requests go through auth check first
    location / {
        auth_request /_speakeasy_verify;
        error_page 401 = /denied;
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
    }
}
```

---

## Deployment Steps

### 1\. VPS Setup

```shell
# On a fresh Ubuntu/Debian VPS
sudo apt update && sudo apt upgrade -y
sudo apt install -y nodejs npm git curl

# Confirm Node version (need 18+)
node --version
```

### 2\. Clone and Install

```shell
git clone https://github.com/youruser/speakeasy.git /opt/speakeasy
cd /opt/speakeasy
npm install --production
```

### 3\. Configure

```shell
cp .env.example .env
# Edit .env — set SPEAKEASY_SECRET and SPEAKEASY_SESSION_SECRET
nano .env
```

### 4\. Install as a systemd Service

```shell
sudo cp systemd/speakeasy.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable speakeasy
sudo systemctl start speakeasy
sudo systemctl status speakeasy
```

The `systemd/speakeasy.service` file:

```
[Unit]
Description=Speakeasy Auth Gateway
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/speakeasy
EnvironmentFile=/opt/speakeasy/.env
ExecStart=/usr/bin/node src/server.js
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

### 5\. Configure Caddy or Nginx

```shell
# Caddy
sudo cp caddy/Caddyfile /etc/caddy/Caddyfile
sudo systemctl reload caddy

# OR Nginx
sudo cp nginx/speakeasy.conf /etc/nginx/sites-available/speakeasy
sudo ln -s /etc/nginx/sites-available/speakeasy /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
```

### 6\. Mint Your First Token

```shell
cd /opt/speakeasy
node cli/mint.js --label "don" --expires 90d --url https://yoursite.com
# Copy the share link and send it to yourself to test
```

### 7\. Test End-to-End

```shell
# Should return 401
curl -I https://yoursite.com/

# Should redirect and set cookie
curl -I "https://yoursite.com/enter?token=<your-token>"

# Health check
curl https://yoursite.com/health
```

---

## Security Considerations

**Secret management**: Never commit `.env` to version control. Use `chmod 600 .env` and ensure it is owned by the service user only.

**HTTPS only**: Speakeasy must run behind TLS. The session cookie is set with `Secure` flag — it will not be transmitted over plain HTTP.

**Cookie hardening**: Session cookies are set with `HttpOnly` (no JS access), `Secure` (HTTPS only), and `SameSite=Strict` (no cross-site sending).

**Token sharing**: Access tokens sent in URLs can appear in server logs, browser history, and referrer headers. For sensitive use cases, instruct recipients to clear their browser history after the initial `/enter` redirect, or build a short-lived token that expires in minutes but issues a long-lived session.

**Denylist latency**: There is a window between adding a label to `revoked.json` and the gateway reloading it (default 60 seconds). For immediate revocation, restart the service: `sudo systemctl restart speakeasy`.

**Log hygiene**: Speakeasy logs token labels (not raw token strings) on access. Rotate logs regularly. Do not log full tokens anywhere.

**Rate limiting**: Add rate limiting at the Nginx/Caddy level on the `/enter` route to prevent token brute-forcing, though this is low-risk given the JWT signature requirement.

---

## Future Enhancements

These are intentionally out of scope for v1 but worth noting for later:

- **Web admin UI**: A minimal dashboard to mint/revoke tokens without SSH access  
- **Token usage tracking**: Log when each token was last used (flat file or SQLite)  
- **Magic link email delivery**: Integration with Resend or Postmark to send share links via email directly from the CLI  
- **Multi-site support**: Run one Speakeasy instance gating multiple apps on the same VPS, each with their own token namespace  
- **Ed25519 tokens**: Upgrade from HS256 to asymmetric signing so the gateway can verify tokens without holding the minting secret  
- **Token families**: Group tokens so revoking a "family" (e.g., a client project) revokes all tokens in that group at once  
- **Docker image**: Single-container deployment for easier portability

---

## Dependencies

```json
{
  "dependencies": {
    "express": "^4.18.0",
    "jsonwebtoken": "^9.0.0",
    "cookie-parser": "^1.4.6",
    "dotenv": "^16.0.0",
    "morgan": "^1.10.0",
    "qrcode": "^1.5.3"
  }
}
```

`qrcode` is the only addition over the original dependency list. It is used server-side to generate a base64 PNG of the invite URL, embedded directly in the admin panel response — no external QR service, no third-party requests.

All dependencies are minimal, well-maintained, and have no native bindings — `npm install` is fast and clean on any Linux VPS.

---

*Speakeasy — you need the password to get in.*  
