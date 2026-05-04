package handlers

import (
	"net/http"
)

func HandleEgypt(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/egypt/", http.StatusMovedPermanently)
}
