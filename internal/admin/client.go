package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client talks to the admin HTTP API. Use NewSocketClient for the CLI's
// unix-socket transport; NewHTTPClient for HTTPS + basic auth (future use).
type Client struct {
	http *http.Client
	base string
}

func NewSocketClient(socketPath string) *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
				},
			},
			Timeout: 30 * time.Second,
		},
		// Host portion is ignored by the unix-socket dialer, but net/http
		// insists on a valid URL.
		base: "http://speakeasy",
	}
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var out HealthResponse
	if err := c.do(ctx, "GET", "/admin/api/health", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) CreateToken(ctx context.Context, req CreateTokenRequest) (*CreateTokenResponse, error) {
	var out CreateTokenResponse
	if err := c.do(ctx, "POST", "/admin/api/tokens", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) ListTokens(ctx context.Context) ([]TokenInfo, error) {
	var out ListTokensResponse
	if err := c.do(ctx, "GET", "/admin/api/tokens", nil, &out); err != nil {
		return nil, err
	}
	return out.Tokens, nil
}

func (c *Client) RevokeToken(ctx context.Context, jti string) error {
	return c.do(ctx, "POST", "/admin/api/tokens/"+jti+"/revoke", nil, nil)
}

func (c *Client) UnrevokeToken(ctx context.Context, jti string) error {
	return c.do(ctx, "POST", "/admin/api/tokens/"+jti+"/unrevoke", nil, nil)
}

// ResolveJTI accepts either a jti or a label and returns the jti. For labels,
// the active (non-revoked) token is used; revoked-only labels return an error.
func (c *Client) ResolveJTI(ctx context.Context, arg string) (string, error) {
	tokens, err := c.ListTokens(ctx)
	if err != nil {
		return "", err
	}
	var activeForLabel string
	for _, t := range tokens {
		if t.JTI == arg {
			return t.JTI, nil
		}
		if t.Label == arg && t.RevokedAt == nil {
			if activeForLabel != "" {
				return "", fmt.Errorf("label %q is ambiguous (multiple active tokens)", arg)
			}
			activeForLabel = t.JTI
		}
	}
	if activeForLabel != "" {
		return activeForLabel, nil
	}
	return "", fmt.Errorf("no active token matches %q", arg)
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeAPIError(resp)
	}
	if out != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func decodeAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var e ErrorResponse
	if err := json.Unmarshal(body, &e); err == nil && e.Error != "" {
		return fmt.Errorf("%s: %s", resp.Status, e.Error)
	}
	return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(body)))
}
