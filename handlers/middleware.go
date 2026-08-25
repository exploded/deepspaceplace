package handlers

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// levelFor picks the log level for a completed request.
//
// logship is pinned at WARN, so this function decides what monitor ever sees.
// It gets one question: can anyone act on this line?
//
//   - 5xx is ours to fix.                                          ERROR
//   - 401/403/429 is a client actively refused -- a bad admin
//     password, a CSRF-rejected post, the login limiter being hit. WARN
//   - Everything else 4xx is the client's problem.                 INFO
//
// A 404 is never a warning. A public site is scanned around the clock and a
// path that was never here is ordinary access-log traffic with nothing to fix.
//
// levelFor deliberately does not take the *http.Request, so the level cannot
// depend on a caller-controlled header even by accident.
//
// Do not reintroduce a "404 from one of our own pages means a broken internal
// link" rule keyed on Referer. It looks like good code and it is not: Referer
// is set by the caller, and the SPA-fingerprinting scanners sweeping /graphql,
// /.vite/manifest.json, /dist/manifest.json and /.well-known/jwks.json send
// "Referer: https://deepspaceplace.com/" to look like in-page XHR. The rule
// hands every scanner a switch labelled "promote my noise to a warning" -- it
// is a client-controlled alerting channel, the exact opposite of the intent.
// The same rule was tried on a-part and defeated in production within hours
// (a-part 68fb4a2): two probes a second apart, same client, same 404, INFO
// without the header and WARN with it. This repo logged 34,520 WARN to
// monitor over the 90 days to 2026-08-09 under that rule.
//
// Find broken internal links with a link checker. The 404s are all still in
// the access log on the box at INFO, still greppable by referer.
func levelFor(status int) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusTooManyRequests:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		slog.Log(r.Context(), levelFor(rec.status), r.Method+" "+r.RequestURI,
			"status", rec.status, "duration", time.Since(start))
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if Prod {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://clearoutside.com https://www.yr.no http://www.skippysky.com.au https://www.bom.gov.au; connect-src 'self' https://cloudflareinsights.com; frame-src https://www.youtube-nocookie.com")
		next.ServeHTTP(w, r)
	})
}

func WWWRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Host, "www.") {
			target := "https://deepspaceplace.com" + r.RequestURI
			http.Redirect(w, r, target, http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func CacheStaticAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
		next.ServeHTTP(w, r)
	})
}
