package handlers

import (
	"bytes"
	"io"
	"net/http"
	"path"
	"strings"
)

// Reports serves the night sky transparency reports that skyq publishes into
// dir by scp from the observatory, adding the site navigation to each page as
// it goes out.
//
// The reports are deliberately self-contained: skyq emails them and opens them
// from disk, so a link back to deepspaceplace.com does not belong in the file.
// Adding the header here instead means every report gets it, including nights
// rendered before skyq had any navigation of its own, and nothing has to be
// re-published when the site's menu changes.
//
// Nothing here is cached for long: index.html is regenerated every morning and
// a night's report can be re-published, so pages go out with no-cache and a
// Last-Modified header, which lets a returning browser revalidate for free.
// Anything that is not an HTML page falls through to a plain file server.
func Reports(dir string) http.Handler {
	root := http.Dir(dir)
	files := http.FileServer(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)
		if name == "/" || strings.HasSuffix(r.URL.Path, "/") {
			// A directory request serves its index page or nothing. Never a
			// listing: the directory is the observatory's, not the site's.
			name = path.Join(name, "index.html")
		}
		if !strings.HasSuffix(name, ".html") {
			files.ServeHTTP(w, r)
			return
		}

		// http.Dir cleans the path and joins it under dir, so "../" cannot
		// escape the reports directory.
		f, err := root.Open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		page, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "could not read report", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeContent(w, r, "", info.ModTime(), bytes.NewReader(withSiteNav(page)))
	})
}

// siteNav is the header injected at the top of every report page.
//
// The reports do not load the site's stylesheets, and pulling Bootstrap into
// them would restyle every table and heading. The header is therefore styled
// inline from the report's own CSS variables (surface, border, text colours),
// so it follows the report's light and dark palettes; the fallbacks are the
// report's light values, for a page that defines none of them. The class
// prefix keeps it clear of anything the report styles.
const siteNav = `<header class="dsp-site">
<style>
.dsp-site{font:12.5px/1 system-ui,-apple-system,"Segoe UI",sans-serif;background:var(--surface-1,#fcfcfb);border-bottom:1px solid var(--border,rgba(11,11,11,.1))}
.dsp-site .dsp-in{max-width:1000px;margin:0 auto;padding:13px 24px;display:flex;align-items:center;gap:8px 20px;flex-wrap:wrap}
.dsp-site a{text-decoration:none;text-transform:uppercase;letter-spacing:.05em;white-space:nowrap}
.dsp-site .dsp-brand{font-weight:600;letter-spacing:.06em;color:var(--text-primary,#0b0b0b);margin-right:auto}
.dsp-site .dsp-link{font-size:11.5px;color:var(--text-secondary,#52514e)}
.dsp-site a:hover{color:var(--series-1,#1f6fd6)}
</style>
<nav class="dsp-in" aria-label="Site">
<a class="dsp-brand" href="/">Deep Space Place</a>
<a class="dsp-link" href="/images">Deep Space</a>
<a class="dsp-link" href="/skymap">Sky Map</a>
<a class="dsp-link" href="/moon">Moon</a>
<a class="dsp-link" href="/weather">Weather</a>
<a class="dsp-link" href="/reports/">Night Reports</a>
</nav>
</header>
`

// withSiteNav returns page with siteNav inserted directly after the opening
// <body> tag. A page with no <body> tag is returned unchanged rather than
// guessed at.
func withSiteNav(page []byte) []byte {
	lower := bytes.ToLower(page)
	start := bytes.Index(lower, []byte("<body"))
	if start < 0 {
		return page
	}
	end := bytes.IndexByte(page[start:], '>')
	if end < 0 {
		return page
	}
	at := start + end + 1

	out := make([]byte, 0, len(page)+len(siteNav)+1)
	out = append(out, page[:at]...)
	out = append(out, '\n')
	out = append(out, siteNav...)
	out = append(out, page[at:]...)
	return out
}
