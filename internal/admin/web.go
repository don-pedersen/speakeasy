package admin

import (
	"embed"
	"net/http"
)

//go:embed views/admin.html
var adminHTML embed.FS

// serveUI writes the embedded admin single-page UI. No authentication is
// enforced here — the caller is expected to wrap the mux with BasicAuth.
func (d Deps) serveUI(w http.ResponseWriter, r *http.Request) {
	data, err := adminHTML.ReadFile("views/admin.html")
	if err != nil {
		http.Error(w, "admin UI missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	_, _ = w.Write(data)
}
