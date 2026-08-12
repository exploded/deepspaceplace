package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"deepspaceplace/internal/database"
)

var TemplateFuncs = template.FuncMap{
	"queryParams": queryParams,
	"asset":       assetURL,
	"add":         func(a, b float64) float64 { return a + b },
	"sub":         func(a, b float64) float64 { return a - b },
	"div":         func(a, b float64) float64 { return a / b },
}

// StaticDir is where CacheStaticAssets serves /static/ from, relative to the
// working directory. A variable so tests can point assetURL at a fixture.
var StaticDir = "static"

// assetVersions caches one content hash per asset path. The files cannot change
// under a running process -- a deploy replaces the binary too -- so each is read
// exactly once.
var assetVersions sync.Map // url path -> query suffix, possibly ""

// assetURL stamps a static asset's URL with a hash of its contents.
//
// CacheStaticAssets serves /static/ as "immutable" with a seven-day max-age.
// That is the right header for bytes that never change under a given name, and
// exactly the wrong one for a stylesheet edited in place: a returning visitor
// keeps the old copy for a week, so a CSS fix ships green and changes nothing
// anyone can see. Putting the hash in the URL makes the name change whenever
// the bytes do, which is the promise "immutable" is actually making.
func assetURL(p string) string {
	if v, ok := assetVersions.Load(p); ok {
		return p + v.(string)
	}

	suffix := ""
	if rel, ok := strings.CutPrefix(p, "/static/"); ok {
		// Reject traversal rather than hashing whatever it reaches. Callers are
		// all template literals today, so this is a guard against a future one
		// built from a request.
		if clean := path.Clean(rel); clean == rel && !strings.HasPrefix(rel, "../") {
			data, err := os.ReadFile(filepath.Join(StaticDir, filepath.FromSlash(rel)))
			if err != nil {
				// Serve the bare path: an unversioned asset is stale for a
				// week, a missing one is a broken page.
				slog.Error("Could not hash static asset for cache busting", "path", p, "error", err)
			} else {
				sum := sha256.Sum256(data)
				suffix = "?v=" + hex.EncodeToString(sum[:])[:10]
			}
		}
	}

	assetVersions.Store(p, suffix)
	return p + suffix
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

type PageData struct {
	CanonicalURL string
	Title        string
	Description  string
}

// Render executes a named template with the "base" definition and the given data.
func Render(w http.ResponseWriter, name string, data interface{}) {
	RenderStatus(w, http.StatusOK, name, data)
}

// RenderStatus is Render with an explicit response status, for pages that are
// also a refusal (the login lockout's 429). The template is executed into a
// buffer before anything is written, so a template failure still becomes a
// clean 500 rather than a partial body under the wrong status.
func RenderStatus(w http.ResponseWriter, status int, name string, data interface{}) {
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
	w.WriteHeader(status)
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

func StaticPage(templateName, pagePath, title, description string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		Render(w, templateName, PageData{
			CanonicalURL: "https://deepspaceplace.com" + pagePath,
			Title:        title,
			Description:  description,
		})
	}
}

func HandleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	Render(w, "index.html", PageData{
		CanonicalURL: "https://deepspaceplace.com/",
	})
}

func HandleFavicon(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/favicon.ico")
}

func HandleRobotsTxt(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/robots.txt")
}

func HandleWebManifest(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "static/site.webmanifest")
}
