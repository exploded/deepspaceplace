package handlers

import (
	"net/http"
	"path/filepath"
)

func HandleEgypt(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, filepath.Join("static", "egypt", "index.html"))
}
