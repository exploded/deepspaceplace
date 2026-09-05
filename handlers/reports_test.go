package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const reportPage = "<!doctype html>\n<html lang=\"en\">\n<head><title>Night</title></head>\n<body>\n<div class=\"wrap\"><h1>Sky transparency</h1></div>\n</body>\n</html>\n"

// reportsFixture lays out a reports directory the way skyq publishes it, plus
// one file outside it that must never be reachable.
func reportsFixture(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "reports")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "index.html"), strings.Replace(reportPage, "Sky transparency", "All nights", 1))
	write(filepath.Join(dir, "2026-09-02.html"), reportPage)
	write(filepath.Join(dir, "notes.txt"), "plain text\n")
	write(filepath.Join(parent, "secret.html"), "<body>outside</body>")
	return dir
}

func getReport(t *testing.T, h http.Handler, p string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	return rec
}

func TestReportsInjectsSiteNavIntoHTML(t *testing.T) {
	h := Reports(reportsFixture(t))

	rec := getReport(t, h, "/2026-09-02.html")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if got := strings.Count(body, `class="dsp-site"`); got != 1 {
		t.Fatalf("site nav appears %d times, want exactly once:\n%s", got, body)
	}
	navAt := strings.Index(body, `<header class="dsp-site">`)
	bodyAt := strings.Index(body, "<body>")
	wrapAt := strings.Index(body, `<div class="wrap">`)
	if !(bodyAt < navAt && navAt < wrapAt) {
		t.Errorf("site nav must sit between <body> and the report's own content (body=%d nav=%d wrap=%d)", bodyAt, navAt, wrapAt)
	}
	for _, href := range []string{`href="/"`, `href="/reports/"`, `href="/images"`} {
		if !strings.Contains(body, href) {
			t.Errorf("site nav missing link %s", href)
		}
	}
	if !strings.HasSuffix(body, "</html>\n") {
		t.Errorf("report content after the nav was not preserved")
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache (reports are re-published in place)", cc)
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Errorf("no Last-Modified header, so browsers cannot revalidate")
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length %q does not match the injected body length %d", cl, len(body))
	}
}

func TestReportsDirectoryServesIndex(t *testing.T) {
	h := Reports(reportsFixture(t))

	// After StripPrefix("/reports/") the directory itself arrives as "" and
	// as "/", depending on how the request was formed.
	for _, p := range []string{"/", "/?x=1"} {
		rec := getReport(t, h, p)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %q status = %d, want 200", p, rec.Code)
			continue
		}
		body := rec.Body.String()
		if !strings.Contains(body, "All nights") || !strings.Contains(body, `class="dsp-site"`) {
			t.Errorf("GET %q did not serve index.html with the site nav", p)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.URL.Path = ""
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "All nights") {
		t.Errorf("empty path after StripPrefix: status = %d, want the index page", rec.Code)
	}
}

func TestReportsNonHTMLIsUntouched(t *testing.T) {
	h := Reports(reportsFixture(t))

	rec := getReport(t, h, "/notes.txt")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := rec.Body.String(); body != "plain text\n" {
		t.Errorf("non-HTML file was modified: %q", body)
	}
}

func TestReportsHidesWhatIsNotAReport(t *testing.T) {
	h := Reports(reportsFixture(t))

	cases := map[string]string{
		"missing report":          "/2026-01-01.html",
		"traversal outside dir":   "/../secret.html",
		"directory with no index": "/nope/",
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			rec := getReport(t, h, p)
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want 404", p, rec.Code)
			}
			if strings.Contains(rec.Body.String(), "outside") {
				t.Errorf("GET %s served a file from outside the reports directory", p)
			}
		})
	}
}

func TestReportsConditionalGet(t *testing.T) {
	h := Reports(reportsFixture(t))

	first := getReport(t, h, "/2026-09-02.html")
	lm := first.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified on first response")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/2026-09-02.html", nil)
	req.Header.Set("If-Modified-Since", lm)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation status = %d, want 304", rec.Code)
	}
}

func TestWithSiteNavLeavesPagesWithoutBodyAlone(t *testing.T) {
	in := []byte("<p>fragment only</p>")
	if got := withSiteNav(in); string(got) != string(in) {
		t.Errorf("page without <body> was changed: %q", got)
	}
	// An attributed, upper-case body tag must still be found.
	got := string(withSiteNav([]byte("<BODY class=\"x\">\n<p>hi</p>")))
	if !strings.HasPrefix(got, "<BODY class=\"x\">\n<header class=\"dsp-site\">") {
		t.Errorf("nav not inserted after an attributed body tag:\n%s", got)
	}
}
