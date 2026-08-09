package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLevelFor is the regression guard for a real incident: every 404 used to
// log at WARN, and background scanning of a public site kept monitor's app log
// permanently full of warnings nobody could act on. Keep bot 404s at INFO.
func TestLevelFor(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		referer string
		want    slog.Level
	}{
		{"server error", 500, "", slog.LevelError},
		{"bad gateway", 502, "", slog.LevelError},
		{"scanner 404", 404, "", slog.LevelInfo},
		{"404 linked from elsewhere", 404, "https://example.com/list", slog.LevelInfo},
		{"404 from our own page", 404, "https://deepspaceplace.com/images", slog.LevelWarn},
		{"404 from www", 404, "https://www.deepspaceplace.com/images", slog.LevelWarn},
		{"failed admin login", 401, "", slog.LevelWarn},
		{"forbidden", 403, "", slog.LevelWarn},
		{"rate limited", 429, "", slog.LevelWarn},
		{"bad request", 400, "", slog.LevelInfo},
		{"ok", 200, "", slog.LevelInfo},
		{"redirect", 301, "", slog.LevelInfo},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com/nope", nil)
			r.Host = "deepspaceplace.com"
			if tc.referer != "" {
				r.Header.Set("Referer", tc.referer)
			}
			if got := levelFor(tc.status, r); got != tc.want {
				t.Errorf("levelFor(%d, referer=%q) = %v, want %v",
					tc.status, tc.referer, got, tc.want)
			}
		})
	}
}

// TestIsInternalRefererIgnoresGarbage makes sure an unparseable Referer from a
// hostile client cannot panic or be mistaken for one of our own pages.
func TestIsInternalRefererIgnoresGarbage(t *testing.T) {
	for _, ref := range []string{"://", "not a url", "http://[::1", "\x00"} {
		r := httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com/nope", nil)
		r.Host = "deepspaceplace.com"
		r.Header.Set("Referer", ref)
		if isInternalReferer(r) {
			t.Errorf("isInternalReferer(%q) = true, want false", ref)
		}
	}
}
