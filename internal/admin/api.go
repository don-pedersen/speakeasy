// Package admin exposes the token-management HTTP API. The same handlers are
// mounted on the unix socket (CLI transport) and the HTTPS admin panel (step 6).
package admin

import "time"

type CreateTokenRequest struct {
	Label   string `json:"label"`
	Route   string `json:"route"`
	Expires string `json:"expires,omitempty"`
	Msg     string `json:"msg,omitempty"`
}

type CreateTokenResponse struct {
	JTI       string     `json:"jti"`
	Token     string     `json:"token"`
	InviteURL string     `json:"invite_url"`
	Label     string     `json:"label"`
	Route     string     `json:"route"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// QRCode is a "data:image/png;base64,..." URL of the invite URL encoded
	// as a QR code. Populated server-side so clients don't need a QR lib.
	QRCode string `json:"qr_code,omitempty"`
}

// RouteInfo is one configured route (returned by GET /admin/api/routes, used
// by the admin UI to populate the mint form's route dropdown).
type RouteInfo struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Upstream string `json:"upstream"`
}

type ListRoutesResponse struct {
	Routes []RouteInfo `json:"routes"`
}

type TokenInfo struct {
	JTI        string     `json:"jti"`
	Label      string     `json:"label"`
	Route      string     `json:"route"`
	Msg        string     `json:"msg,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	Active     bool       `json:"active"`
}

type ListTokensResponse struct {
	Tokens []TokenInfo `json:"tokens"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}
