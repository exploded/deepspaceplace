package handlers

import (
	"log/slog"
	"net/http"
	"net/url"
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
// A public site is scanned around the clock, so a 404 for something that was
// never here is ordinary access-log traffic, not a warning -- logging it at
// WARN buries the handful of requests that do need attention. What still earns
// a warning is a client being refused (401/403/429) and a 404 reached from one
// of our own pages, which means a broken internal link.
func levelFor(status int, r *http.Request) slog.Level {
	switch {
	case status >= 500:
		return slog.LevelError
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusTooManyRequests:
		return slog.LevelWarn
	case status == http.StatusNotFound && isInternalReferer(r):
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// isInternalReferer reports whether the request was linked from one of our own
// pages, as opposed to typed, crawled or probed.
func isInternalReferer(r *http.Request) bool {
	ref := r.Referer()
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	return u.Host == r.Host || strings.TrimPrefix(u.Host, "www.") == "deepspaceplace.com"
}

func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		slog.Log(r.Context(), levelFor(rec.status, r), r.Method+" "+r.RequestURI,
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
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' https://static.cloudflareinsights.com; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://clearoutside.com https://www.yr.no http://www.skippysky.com.au https://www.bom.gov.au; connect-src 'self'; frame-src https://www.youtube-nocookie.com")
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
