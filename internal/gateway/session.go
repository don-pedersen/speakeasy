package gateway

import (
	"errors"
	"net/http"
	"time"

	"speakeasy/internal/store"
)

// checkSession validates the session cookie against a given route name.
// Returns the session and true if all checks pass; otherwise writes nothing
// (the caller is responsible for the denial response) and returns false.
func (g *Gateway) checkSession(w http.ResponseWriter, r *http.Request, routeName string) (*store.Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	sess, err := g.store.GetSession(r.Context(), c.Value)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			g.clearCookie(w)
		}
		return nil, false
	}
	now := time.Now().UTC()
	if !sess.ExpiresAt.After(now) {
		_ = g.store.DeleteSession(r.Context(), sess.ID)
		g.clearCookie(w)
		return nil, false
	}
	// Route enforcement: a session is good for exactly one route.
	if sess.Route != routeName {
		return nil, false
	}
	// Best-effort last-seen touch (non-blocking failure).
	_ = g.store.TouchSessionLastSeen(r.Context(), sess.ID)
	return sess, true
}

// issueSession persists a session row and sets the cookie on the response.
func (g *Gateway) issueSession(w http.ResponseWriter, r *http.Request, tokenID int64, route string) (*store.Session, error) {
	sess, err := g.store.CreateSession(r.Context(), tokenID, route, g.sessionTTL)
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    sess.ID,
		Path:     "/",
		Expires:  sess.ExpiresAt,
		HttpOnly: true,
		Secure:   g.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
	return sess, nil
}

func (g *Gateway) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   g.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}
