package handlers

import (
	"html/template"
	"log"
	"net/http"

	"deepspaceplace/internal/database"
)

var Templates map[string]*template.Template
var DB *database.Queries

// Render executes a named template with the "base" definition and the given data.
func Render(w http.ResponseWriter, name string, data interface{}) {
	t, ok := Templates[name]
	if !ok {
		log.Printf("Template %s not found", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base", data); err != nil {
		log.Printf("Error executing template %s: %v", name, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// RenderPartial executes a named partial template (no base layout).
func RenderPartial(w http.ResponseWriter, hostTemplate, partialName string, data interface{}) {
	t, ok := Templates[hostTemplate]
	if !ok {
		log.Printf("Template %s not found for partial %s", hostTemplate, partialName)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, partialName, data); err != nil {
		log.Printf("Error executing partial %s: %v", partialName, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func StaticPage(templateName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Render(w, templateName, nil)
	}
}

func HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	Render(w, "index.html", nil)
}

func HandleFavicon(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/favicon.ico")
}

func HandleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/robots.txt")
}
