package handlers

import (
	"net/http"
	"strings"
)

// HandlePHPRedirect permanently redirects old .php URLs to their new equivalents.
// Query params like sort/filter are stripped — only essential params (e.g. id) are kept.
func HandlePHPRedirect(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Strip .php extension
	name := strings.TrimSuffix(path, ".php")
	name = strings.TrimPrefix(name, "/")

	// Special cases where the route name changed
	switch name {
	case "index":
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
		return
	case "coordinate-converter":
		http.Redirect(w, r, "/converter", http.StatusMovedPermanently)
		return
	case "bom_satellite_proxy":
		http.Redirect(w, r, "/api/bom-satellite", http.StatusMovedPermanently)
		return
	case "images":
		http.Redirect(w, r, "/images", http.StatusMovedPermanently)
		return
	}

	// show.php?id=X → /show?id=X (keep only id param)
	if name == "show" {
		id := r.URL.Query().Get("id")
		if id != "" {
			http.Redirect(w, r, "/show?id="+id, http.StatusMovedPermanently)
			return
		}
		http.Redirect(w, r, "/images", http.StatusMovedPermanently)
		return
	}

	// All other .php pages map directly: /foo.php → /foo
	http.Redirect(w, r, "/"+name, http.StatusMovedPermanently)
}
