package handlers

import (
	"bytes"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"deepspaceplace/internal/database"
)

var TemplateFuncs = template.FuncMap{
	"queryParams": queryParams,
}

func queryParams(pairs ...string) template.URL {
	q := url.Values{}
	for i := 0; i+1 < len(pairs); i += 2 {
		if pairs[i+1] != "" {
			q.Set(pairs[i], pairs[i+1])
		}
	}
	if len(q) == 0 {
		return ""
	}
	return template.URL("?" + q.Encode())
}

// redirectIfEmptyParams strips empty query parameters and issues a 301 redirect
// if the cleaned URL differs from the original. Returns true if a redirect was sent.
func redirectIfEmptyParams(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	clean := url.Values{}
	for k, vals := range q {
		for _, v := range vals {
			if v != "" {
				clean.Add(k, v)
			}
		}
	}
	if len(clean) == len(q) {
		return false
	}
	target := r.URL.Path
	if len(clean) > 0 {
		target += "?" + clean.Encode()
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
	return true
}

var Templates map[string]*template.Template
var DB *database.Queries
var Prod bool

// Render executes a named template with the "base" definition and the given data.
func Render(w http.ResponseWriter, name string, data interface{}) {
	t, ok := Templates[name]
	if !ok {
		slog.Error("Template not found", "name", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", data); err != nil {
		slog.Error("Error executing template", "name", name, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// RenderPartial executes a named partial template (no base layout).
func RenderPartial(w http.ResponseWriter, hostTemplate, partialName string, data interface{}) {
	t, ok := Templates[hostTemplate]
	if !ok {
		slog.Error("Template not found for partial", "template", hostTemplate, "partial", partialName)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, partialName, data); err != nil {
		slog.Error("Error executing partial", "partial", partialName, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
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
