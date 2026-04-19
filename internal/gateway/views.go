package gateway

import (
	"embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed views/*.html
var viewFS embed.FS

// Parsed once at package init. Failure is a programming error, not a runtime
// one — panic is appropriate.
var templates = template.Must(template.ParseFS(viewFS, "views/*.html"))

// inviteLandingData drives views/invite.html.
type inviteLandingData struct {
	SiteName string
	Msg      string
	Path     string // where to redirect (route mount)
	Seconds  int
}

// inviteErrorData drives views/invite_error.html.
type inviteErrorData struct {
	SiteName string
	Title    string
	Detail   string
}

const inviteCountdownSeconds = 5

func (g *Gateway) renderInviteLanding(w http.ResponseWriter, data inviteLandingData) {
	if data.Seconds == 0 {
		data.Seconds = inviteCountdownSeconds
	}
	render(w, http.StatusOK, "invite.html", data)
}

func (g *Gateway) renderInviteError(w http.ResponseWriter, status int, title, detail string) {
	render(w, status, "invite_error.html", inviteErrorData{
		SiteName: g.cfg.Server.SiteName,
		Title:    title,
		Detail:   detail,
	})
}

// renderDenied writes the bare denied page. Intentionally takes no detail —
// this endpoint must not leak site info (step-4 design constraint).
func (g *Gateway) renderDenied(w http.ResponseWriter, status int) {
	render(w, status, "denied.html", nil)
}

func render(w http.ResponseWriter, status int, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := templates.ExecuteTemplate(w, name, data); err != nil {
		// Headers are already sent; logging is all we can do.
		log.Printf("render %s: %v", name, err)
	}
}
