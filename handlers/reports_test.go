package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"deepspaceplace/internal/database"

	"github.com/exploded/nightreport"
)

const reportPage = "<!doctype html>\n<html lang=\"en\">\n<head><title>Night</title></head>\n<body>\n<div class=\"wrap\"><h1>Sky transparency</h1>\n<p class=\"sub\">Cloud arrived at <b>02:15</b> &mdash; late.</p></div>\n</body>\n</html>\n"

const (
	testSkyqKey   = "skyq-test-key"
	testSkycamKey = "skycam-test-key"
)

// reportsFixture lays out a reports directory the way the producers
// published HTML before the API, plus one file outside it that must never be
// reachable.
func reportsFixture(t *testing.T) string {
	t.Helper()
	parent := t.TempDir()
	dir := filepath.Join(parent, "reports")
	if err := os.MkdirAll(filepath.Join(dir, "skycam"), 0o755); err != nil {
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
	write(filepath.Join(dir, "skycam", "2026-09-02.html"), reportPage)
	write(filepath.Join(dir, "skycam", "2026-10-30.html"), strings.Replace(reportPage, "Cloud arrived", "Roof shut", 1))
	write(filepath.Join(dir, "notes.txt"), "plain text\n")
	write(filepath.Join(parent, "secret.html"), "<body>outside</body>")
	return dir
}

// withReports swaps in a fresh database, the real report templates and the
// test keys, and returns a handler that routes like main.go: the upload API
// plus /reports/.
func withReports(t *testing.T) http.Handler {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "_")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	schema, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}

	templates := map[string]*template.Template{}
	for _, name := range []string{"reports.html", "report_night.html"} {
		templates[name] = template.Must(template.New("").Funcs(TemplateFuncs).ParseFiles(
			filepath.Join("..", "templates", "base.html"), filepath.Join("..", "templates", name)))
	}

	oldDB, oldTemplates, oldKeys := DB, Templates, ReportKeys
	DB, Templates = database.New(conn), templates
	ReportKeys = map[string]string{"skyq": testSkyqKey, "skycam": testSkycamKey}
	t.Cleanup(func() { DB, Templates, ReportKeys = oldDB, oldTemplates, oldKeys })

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/reports/{source}/{night}", HandleReportUpload)
	mux.Handle("/reports/", http.StripPrefix("/reports/", Reports(reportsFixture(t))))
	return mux
}

func get(t *testing.T, h http.Handler, p string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
	return rec
}

func put(t *testing.T, h http.Handler, p, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, p, bytes.NewReader(body))
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func skyqReport(night string) nightreport.Report {
	ref := 100.0
	return nightreport.Report{
		Schema: nightreport.Schema, Source: nightreport.SourceSkyq, Night: night,
		Title: "Imaging session", Verdict: "Cloud arrived at 02:15.", Summary: "Cloud arrived at 02:15.",
		Pills: []string{"Index 104", "Cloud 02:15"},
		Stats: []nightreport.Stat{{K: "Index before 02:15", V: "104", Small: "IQR 94–113"}},
		Sections: []nightreport.Section{{
			Heading: "Normalised transparency index, by filter",
			Legend:  []nightreport.LegendItem{{Label: "H α", Slot: 1}, {Label: "Plate solve failed", Mark: nightreport.MarkSolve}},
			Chart: &nightreport.Chart{
				H: 320, YMax: 200, YTicks: 4, YFmt: "0", YLabel: "% of clear-sky median", GapMin: 75, Rail: true,
				Ref: &ref, RefLabel: "clear-sky baseline", SpanMin: 480, OriginMin: 21*60 + 45, TickStart: 15, Hover: "pct",
				Series: []nightreport.Series{{Name: "H α", Label: "H", Slot: 1, Pts: []nightreport.Pt{{M: 10, V: 100, Stars: 300, Target: "IC 434"}}}},
			},
			Images: []nightreport.Image{{Src: "data:image/jpeg;base64,AAAA", Alt: "All-sky at 02:15", Caption: "02:15", Note: "cloud onset"}},
			Events: []nightreport.Event{{Time: "02:20:00", Kind: "solve failed", Detail: "no stars", Cause: "cloud"}},
			Table:  &nightreport.Table{Summary: "All 1 frames", Columns: []string{"Time", "Stars"}, Rows: [][]string{{"22:00", "300"}}},
		}},
		GeneratedAt: time.Date(2026, 9, 3, 23, 0, 0, 0, time.UTC),
	}
}

func skycamReport(night string) nightreport.Report {
	return nightreport.Report{
		Schema: nightreport.Schema, Source: nightreport.SourceSkycam, Night: night,
		Title: "All-sky camera", Verdict: "Roof open for 6h 10m.", Summary: "Roof open for 6h 10m.",
		Sections: []nightreport.Section{
			{Heading: "Timelapse", Video: &nightreport.Video{Src: "https://deepspaceplace-allsky.s3.ap-southeast-2.amazonaws.com/allsky/videos/allsky-20260902.mp4"}},
			{Heading: "Keogram", Images: []nightreport.Image{{Src: "https://deepspaceplace-allsky.s3.ap-southeast-2.amazonaws.com/allsky/keograms/keogram-20260902.jpg", Alt: "Keogram", Caption: "Keogram", Wide: true}}},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ── upload API ──────────────────────────────────────────────────────────

func TestReportUploadStoresAndReplaces(t *testing.T) {
	h := withReports(t)
	rep := skyqReport("2026-09-02")
	if rec := put(t, h, "/api/reports/skyq/2026-09-02", testSkyqKey, mustJSON(t, rep)); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	rep.Verdict = "Re-published verdict."
	if rec := put(t, h, "/api/reports/skyq/2026-09-02", testSkyqKey, mustJSON(t, rep)); rec.Code != http.StatusNoContent {
		t.Fatalf("second PUT status = %d: %s", rec.Code, rec.Body)
	}
	rows, err := DB.GetNightReports(t.Context(), "2026-09-02")
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one row after re-publishing, got %d (%v)", len(rows), err)
	}
	if !strings.Contains(rows[0].Body, "Re-published verdict.") {
		t.Fatalf("second PUT did not replace the first: %s", rows[0].Body)
	}
}

func TestReportUploadRefusesBadKeys(t *testing.T) {
	h := withReports(t)
	skyq := mustJSON(t, skyqReport("2026-09-02"))
	skycam := mustJSON(t, skycamReport("2026-09-02"))
	cases := []struct {
		name, path, key string
		body            []byte
	}{
		{"no key", "/api/reports/skyq/2026-09-02", "", skyq},
		{"wrong key", "/api/reports/skyq/2026-09-02", "guess", skyq},
		{"skyq key on skycam", "/api/reports/skycam/2026-09-02", testSkyqKey, skycam},
		{"unknown source", "/api/reports/nina/2026-09-02", testSkyqKey, skyq},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := put(t, h, c.path, c.key, c.body); rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
		})
	}

	ReportKeys["skycam"] = ""
	if rec := put(t, h, "/api/reports/skycam/2026-09-02", "", skycam); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a source with no key configured accepted an upload: %d", rec.Code)
	}
	if rows, _ := DB.GetNightReports(t.Context(), "2026-09-02"); len(rows) != 0 {
		t.Fatalf("refused uploads stored %d rows", len(rows))
	}
}

func TestReportUploadRejectsBadReports(t *testing.T) {
	h := withReports(t)
	mutate := func(f func(r *nightreport.Report)) []byte {
		r := skyqReport("2026-09-02")
		f(&r)
		return mustJSON(t, r)
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	cases := []struct {
		name, path string
		body       []byte
		want       int
	}{
		{"not JSON", "/api/reports/skyq/2026-09-02", []byte("<html>"), 400},
		{"night mismatch", "/api/reports/skyq/2026-09-03", mutate(func(r *nightreport.Report) {}), 400},
		{"source mismatch", "/api/reports/skyq/2026-09-02", mutate(func(r *nightreport.Report) { r.Source = "skycam" }), 400},
		{"bad night", "/api/reports/skyq/..%2F..%2Fetc", mutate(func(r *nightreport.Report) {}), 400},
		{"too old", "/api/reports/skyq/2019-01-01", mutate(func(r *nightreport.Report) { r.Night = "2019-01-01" }), 400},
		{"future", "/api/reports/skyq/" + tomorrow, mutate(func(r *nightreport.Report) { r.Night = tomorrow }), 400},
		{"wrong schema", "/api/reports/skyq/2026-09-02", mutate(func(r *nightreport.Report) { r.Schema = 99 }), 400},
		{"javascript image", "/api/reports/skyq/2026-09-02", mutate(func(r *nightreport.Report) {
			r.Sections[0].Images[0].Src = "javascript:alert(1)"
		}), 400},
		{"foreign image host", "/api/reports/skyq/2026-09-02", mutate(func(r *nightreport.Report) {
			r.Sections[0].Images[0].Src = "https://evil.example/x.jpg"
		}), 400},
		{"oversize", "/api/reports/skyq/2026-09-02", mutate(func(r *nightreport.Report) {
			r.Foot = strings.Repeat("x", maxReportBody)
		}), 413},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if rec := put(t, h, c.path, testSkyqKey, c.body); rec.Code != c.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.want, rec.Body)
			}
		})
	}
	if rows, _ := DB.ListNightReportSummaries(t.Context()); len(rows) != 0 {
		t.Fatalf("rejected uploads stored %d rows", len(rows))
	}
}

func TestReportUploadOnlyAcceptsPut(t *testing.T) {
	h := withReports(t)
	rec := get(t, h, "/api/reports/skyq/2026-09-02")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET on the upload API = %d, want 405", rec.Code)
	}
}

// ── night page ──────────────────────────────────────────────────────────

func TestNightPageShowsBothReports(t *testing.T) {
	h := withReports(t)
	put(t, h, "/api/reports/skyq/2026-09-02", testSkyqKey, mustJSON(t, skyqReport("2026-09-02")))
	put(t, h, "/api/reports/skycam/2026-09-02", testSkycamKey, mustJSON(t, skycamReport("2026-09-02")))

	rec := get(t, h, "/reports/2026-09-02")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"Night of 2–3 September 2026",
		`<section id="imaging">`, `<section id="sky">`,
		"Cloud arrived at 02:15.", "Roof open for 6h 10m.",
		`<svg id="imaging-chart-0"`, `class="nr-hover"`,
		`style="background:var(--series-1)"`, // legend swatch survives html/template
		`src="data:image/jpeg;base64,AAAA"`,
		`allsky-20260902.mp4`, `keogram-20260902.jpg`,
		`class="navbar`, // the site's own layout
	} {
		if !strings.Contains(body, want) {
			t.Errorf("night page lacks %s", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("html/template rejected a value on the night page")
	}
	if strings.Index(body, `id="imaging"`) > strings.Index(body, `id="sky"`) {
		t.Error("imaging must come before sky")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q", cc)
	}
	if rec.Header().Get("Last-Modified") == "" {
		t.Error("no Last-Modified")
	}
}

func TestNightPageEscapesReportText(t *testing.T) {
	h := withReports(t)
	rep := skyqReport("2026-09-02")
	rep.Verdict = `<script>alert(1)</script>`
	rep.Sections[0].Chart.Series[0].Name = `</script><script>alert(2)</script>`
	rep.Sections[0].Events[0].Detail = `<img src=x onerror=alert(3)>`
	if rec := put(t, h, "/api/reports/skyq/2026-09-02", testSkyqKey, mustJSON(t, rep)); rec.Code != http.StatusNoContent {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	body := get(t, h, "/reports/2026-09-02").Body.String()
	for _, bad := range []string{"<script>alert", "<img src=x", "</script><script>"} {
		if strings.Contains(body, bad) {
			t.Errorf("report text reached the page unescaped: %s", bad)
		}
	}
}

func TestNightPageSkyOnlyLinksOldImagingReport(t *testing.T) {
	h := withReports(t)
	put(t, h, "/api/reports/skycam/2026-09-02", testSkycamKey, mustJSON(t, skycamReport("2026-09-02")))

	body := get(t, h, "/reports/2026-09-02").Body.String()
	if !strings.Contains(body, `<section id="sky">`) || strings.Contains(body, `<section id="imaging">`) {
		t.Fatal("want the sky report alone")
	}
	if !strings.Contains(body, `href="/reports/2026-09-02.html"`) {
		t.Error("the not-yet-republished imaging report is not linked")
	}
	// The old skyq URL still serves the old page, since skyq has not
	// re-published this night.
	if rec := get(t, h, "/reports/2026-09-02.html"); rec.Code != http.StatusOK {
		t.Errorf("old imaging page = %d, want 200", rec.Code)
	}
	// The old skycam URL redirects: skycam has.
	rec := get(t, h, "/reports/skycam/2026-09-02.html")
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/reports/2026-09-02" {
		t.Errorf("old skycam page = %d → %q, want 301 → /reports/2026-09-02", rec.Code, rec.Header().Get("Location"))
	}
}

func TestNightPageWithoutReports(t *testing.T) {
	h := withReports(t)
	if rec := get(t, h, "/reports/2026-01-01"); rec.Code != http.StatusNotFound {
		t.Errorf("night with nothing = %d, want 404", rec.Code)
	}
	rec := get(t, h, "/reports/2026-10-30")
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/reports/skycam/2026-10-30.html" {
		t.Errorf("legacy-only night = %d → %q, want 302 to the old page", rec.Code, rec.Header().Get("Location"))
	}
}

// ── index ───────────────────────────────────────────────────────────────

func TestReportIndexMergesSourcesNewestFirst(t *testing.T) {
	h := withReports(t)
	put(t, h, "/api/reports/skyq/2026-09-02", testSkyqKey, mustJSON(t, skyqReport("2026-09-02")))
	put(t, h, "/api/reports/skycam/2026-11-04", testSkycamKey, mustJSON(t, skycamReport("2026-11-04")))

	for _, p := range []string{"/reports/", "/reports/index.html"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", p, rec.Code)
		}
		body := rec.Body.String()
		nov, oct, sep := strings.Index(body, "Wed 4–5 Nov"), strings.Index(body, "Fri 30–31 Oct"), strings.Index(body, "Wed 2–3 Sep")
		if nov < 0 || oct < 0 || sep < 0 || !(nov < oct && oct < sep) {
			t.Fatalf("nights missing or out of order (nov=%d oct=%d sep=%d)", nov, oct, sep)
		}
		for _, want := range []string{
			`href="/reports/2026-09-02#imaging"`, `<span class="pill">Index 104</span>`,
			`href="/reports/skycam/2026-09-02.html"`, // sky for that night is still the old page
			`href="/reports/skycam/2026-10-30.html"`, // legacy-only night links straight to its file
			"Roof shut at 02:15 — late.",             // summary scraped from the old page, as text
			`href="/reports/2026-11-04#sky"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("GET %s lacks %s", p, want)
			}
		}
	}
}

func TestReportsSkycamDirectoryRedirects(t *testing.T) {
	h := withReports(t)
	for _, p := range []string{"/reports/skycam/", "/reports/skycam/index.html"} {
		rec := get(t, h, p)
		if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/reports/" {
			t.Errorf("GET %s = %d → %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
}

// ── legacy HTML ─────────────────────────────────────────────────────────

func TestReportsInjectsSiteNavIntoHTML(t *testing.T) {
	h := withReports(t)

	rec := get(t, h, "/reports/2026-09-02.html")
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
	if !strings.HasSuffix(body, "</html>\n") {
		t.Errorf("report content after the nav was not preserved")
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

func TestReportsNonHTMLIsUntouched(t *testing.T) {
	h := withReports(t)
	rec := get(t, h, "/reports/notes.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != "plain text\n" {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
}

func TestReportsHidesWhatIsNotAReport(t *testing.T) {
	h := withReports(t)
	for name, p := range map[string]string{
		"missing report":          "/reports/2026-01-01.html",
		"not a night":             "/reports/secret.html",
		"traversal outside dir":   "/reports/../secret.html",
		"directory with no index": "/reports/nope/",
		"directory listing":       "/reports/skycam/x/",
	} {
		t.Run(name, func(t *testing.T) {
			rec := get(t, h, p)
			if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "outside") {
				t.Errorf("GET %s = %d, want it refused", p, rec.Code)
			}
		})
	}
}

func TestReportsConditionalGet(t *testing.T) {
	h := withReports(t)
	first := get(t, h, "/reports/2026-09-02.html")
	lm := first.Header().Get("Last-Modified")
	if lm == "" {
		t.Fatal("no Last-Modified on first response")
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/reports/2026-09-02.html", nil)
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
	got := string(withSiteNav([]byte("<BODY class=\"x\">\n<p>hi</p>")))
	if !strings.HasPrefix(got, "<BODY class=\"x\">\n<header class=\"dsp-site\">") {
		t.Errorf("nav not inserted after an attributed body tag:\n%s", got)
	}
}
