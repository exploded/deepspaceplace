package handlers

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLevelFor is the regression guard for a real incident: every 404 used to
// log at WARN, and background scanning of a public site kept monitor's app log
// permanently full of warnings nobody could act on. Keep bot 404s at INFO.
func TestLevelFor(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   slog.Level
	}{
		{"server error", 500, slog.LevelError},
		{"bad gateway", 502, slog.LevelError},
		{"scanner 404", 404, slog.LevelInfo},
		{"scanner probing wp-admin", 404, slog.LevelInfo},
		{"failed admin login", 401, slog.LevelWarn},
		{"forbidden", 403, slog.LevelWarn},
		{"rate limited", 429, slog.LevelWarn},
		{"bad request", 400, slog.LevelInfo},
		{"gone", 410, slog.LevelInfo},
		{"ok", 200, slog.LevelInfo},
		{"redirect", 301, slog.LevelInfo},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := levelFor(tc.status); got != tc.want {
				t.Errorf("levelFor(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestRefererCannotPromoteA404 is the regression guard for the rule this
// replaced.
//
// levelFor used to return WARN for a 404 whose Referer was one of our own
// pages, on the theory that it meant a broken internal link. Referer is set by
// the caller, so scanners sending "Referer: https://deepspaceplace.com/" to
// look like in-page XHR promoted their own noise to WARN -- and because logship
// is pinned at WARN, straight into monitor. Every referer shape must give the
// same INFO on a 404.
//
// This goes through RequestLogger rather than calling levelFor directly, so it
// still fails if someone reintroduces the rule at the middleware layer.
func TestRefererCannotPromoteA404(t *testing.T) {
	referers := []string{
		"",                                      // typed, crawled or probed
		"https://deepspaceplace.com/images",     // our own host
		"https://www.deepspaceplace.com/images", // our own www host
		"https://deepspaceplace.com/",           // what the scanners actually send
		"https://www.google.com/search?q=andromeda", // external
		"://", // unparseable
		"not a url",
		"http://[::1",
		"\x00",
	}

	for _, ref := range referers {
		t.Run(refName(ref), func(t *testing.T) {
			if got := serve404(t, ref); !strings.Contains(got, "level=INFO") {
				t.Errorf("404 with Referer %q logged as %q, want level=INFO", ref, strings.TrimSpace(got))
			}
		})
	}
}

func refName(ref string) string {
	if ref == "" {
		return "absent"
	}
	return strings.Map(func(r rune) rune {
		if r < ' ' || r == ' ' || r == '/' {
			return '_'
		}
		return r
	}, ref)
}

// serve404 runs one 404 through RequestLogger with the given Referer and
// returns the log line it produced.
func serve404(t *testing.T, referer string) string {
	t.Helper()

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })

	h := RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	r := httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com/wp-admin/install.php", nil)
	r.Host = "deepspaceplace.com"
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	h.ServeHTTP(httptest.NewRecorder(), r)

	return buf.String()
}
