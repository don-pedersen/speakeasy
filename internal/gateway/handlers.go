package gateway

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"speakeasy/internal/store"
)

// handleHealth returns a plain 200 OK and never requires auth.
func (g *Gateway) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "ok\n")
}

// handleInvite renders the polished invitation landing page. On success, the
// session cookie is already set and the page auto-redirects after 5 seconds.
func (g *Gateway) handleInvite(w http.ResponseWriter, r *http.Request) {
	tk, rh, terr := g.redeemInvite(w, r)
	if terr != nil {
		g.renderInviteError(w, terr.status, terr.title, terr.detail)
		return
	}
	g.renderInviteLanding(w, inviteLandingData{
		SiteName: g.cfg.Server.SiteName,
		Msg:      tk.Msg,
		Path:     rh.route.Path,
	})
}

// handleEnter validates the same way as /invite but skips the landing and
// immediately redirects. Useful for recipients who've already seen a landing
// once, or for programmatic clients.
func (g *Gateway) handleEnter(w http.ResponseWriter, r *http.Request) {
	_, rh, terr := g.redeemInvite(w, r)
	if terr != nil {
		g.renderInviteError(w, terr.status, terr.title, terr.detail)
		return
	}
	http.Redirect(w, r, rh.route.Path, http.StatusFound)
}

// inviteFailure carries a human-facing error title + detail plus the HTTP
// status to return. Internal — never serialized.
type inviteFailure struct {
	status int
	title  string
	detail string
}

// redeemInvite parses the token query param, validates signature/revocation/
// expiry/route existence, creates a session, touches last_used, and sets the
// cookie. On success it returns the token row and the route handler. On
// failure nothing is written to w.
func (g *Gateway) redeemInvite(w http.ResponseWriter, r *http.Request) (*store.Token, *routeHandler, *inviteFailure) {
	tokenStr := r.URL.Query().Get("token")
	if tokenStr == "" {
		return nil, nil, &inviteFailure{http.StatusBadRequest,
			"No invite link provided.",
			"This URL is missing a token. Use the full link you were sent."}
	}

	claims, err := g.signer.Verify(tokenStr)
	if err != nil {
		g.logf(r, "invite rejected: %v", err)
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, nil, &inviteFailure{http.StatusUnauthorized,
				"This invite has expired.",
				"Ask whoever shared it with you for a fresh link."}
		}
		return nil, nil, &inviteFailure{http.StatusUnauthorized,
			"This invite link is not valid.",
			"The link may have been altered. Paste it again from the original message."}
	}

	tk, err := g.store.GetTokenByJTI(r.Context(), claims.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil, &inviteFailure{http.StatusUnauthorized,
				"This invite is not recognized.",
				"It may have been removed by the site owner."}
		}
		g.logf(r, "store lookup: %v", err)
		return nil, nil, &inviteFailure{http.StatusInternalServerError,
			"Something went wrong.",
			"Please try again in a moment."}
	}
	if !tk.Active(time.Now()) {
		var detail string
		if tk.RevokedAt != nil {
			detail = "This invite has been revoked."
		} else {
			detail = "This invite has expired."
		}
		return nil, nil, &inviteFailure{http.StatusUnauthorized,
			"This invite is no longer active.", detail}
	}

	rh := g.routeByName(tk.Route)
	if rh == nil {
		g.logf(r, "route %q referenced by token no longer exists", tk.Route)
		return nil, nil, &inviteFailure{http.StatusGone,
			"This invite is no longer available.",
			"The resource it points to has been removed."}
	}

	if _, err := g.issueSession(w, r, tk.ID, tk.Route); err != nil {
		g.logf(r, "create session: %v", err)
		return nil, nil, &inviteFailure{http.StatusInternalServerError,
			"Something went wrong.",
			"Please try again in a moment."}
	}
	_ = g.store.TouchTokenLastUsed(r.Context(), tk.ID)
	g.logf(r, "invite accepted label=%s route=%s", tk.Label, tk.Route)
	return tk, rh, nil
}
