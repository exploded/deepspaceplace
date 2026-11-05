package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/exploded/nightreport"
)

// Reports serves the observatory's night reports under /reports/.
//
// Two producers publish them through HandleReportUpload: skyq (the imaging
// session) and skycam (the all-sky camera). Each night gets one page,
// /reports/YYYY-MM-DD, with whichever of the two reports exist, rendered in
// the site's own layout. /reports/ lists every night.
//
// Before the API, both producers wrote finished HTML into dir:
// YYYY-MM-DD.html from skyq and skycam/YYYY-MM-DD.html from skycam. Those
// files are the fallback. An old URL redirects to the new page once that
// source has re-published the night, and until then the old file is served
// with the site header injected, so no link breaks during the switch.
//
// Pages go out no-cache with Last-Modified: a night can be re-published at
// any time, and the 7-day immutable cache on /static/ would pin stale pages.
func Reports(dir string) http.Handler {
	legacy := &legacyReports{dir: dir, cache: map[string]legacySummary{}}
	root := http.Dir(dir)
	files := http.FileServer(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		switch {
		case p == "" || p == "index.html":
			serveReportIndex(w, r, legacy)
		case p == legacySkycamDir || p == legacySkycamDir+"/index.html":
			http.Redirect(w, r, "/reports/", http.StatusMovedPermanently)
		case nightPattern.MatchString(p):
			serveNight(w, r, p, legacy)
		case strings.HasSuffix(p, ".html"):
			serveLegacyURL(w, r, p, legacy)
		default:
			// Anything else a legacy page might reference. Never a directory
			// listing: the directory is the observatory's, not the site's.
			f, err := root.Open("/" + p)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			info, err := f.Stat()
			f.Close()
			if err != nil || info.IsDir() {
				http.NotFound(w, r)
				return
			}
			files.ServeHTTP(w, r)
		}
	})
}

const legacySkycamDir = "skycam"

var (
	nightPattern      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	legacyFilePattern = regexp.MustCompile(`^(?:(skycam)/)?(\d{4}-\d{2}-\d{2})\.html$`)
)

// serveLegacyURL handles an old report URL: YYYY-MM-DD.html (skyq) or
// skycam/YYYY-MM-DD.html. It redirects to the night page once that source
// has a stored report, and otherwise serves the old file.
func serveLegacyURL(w http.ResponseWriter, r *http.Request, p string, legacy *legacyReports) {
	m := legacyFilePattern.FindStringSubmatch(p)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	source, night := nightreport.SourceSkyq, m[2]
	if m[1] != "" {
		source = nightreport.SourceSkycam
	}
	rows, err := DB.GetNightReports(r.Context(), night)
	if err != nil {
		slog.Error("Reading night reports", "night", night, "error", err)
	}
	for _, row := range rows {
		if row.Source == source {
			http.Redirect(w, r, "/reports/"+night, http.StatusMovedPermanently)
			return
		}
	}
	legacy.serve(w, r, p)
}

// ── night page ──────────────────────────────────────────────────────────

type reportNav struct {
	Href, Label string
}

type nightPage struct {
	PageData
	Night, Label string
	Prev, Next   *reportNav
	Imaging, Sky *reportView
	// Old-format pages for a source that has not re-published this night.
	LegacyImaging, LegacySky string
	CSS                      template.CSS
	Script                   template.JS
}

type reportView struct {
	Anchor    string
	Title     string
	Verdict   string
	Stats     []nightreport.Stat
	Sections  []sectionView
	Foot      string
	Generated string
}

type sectionView struct {
	Heading string
	Notes   []string
	Legend  []legendView
	Video   *videoView
	SVG     template.HTML
	Hover   template.JS
	Images  []imageView
	Events  []nightreport.Event
	Table   *nightreport.Table
}

// legendView's Color is template.CSS because html/template rejects a
// var(--series-1) reference in a style attribute and substitutes ZgotmplZ.
// It comes from SlotColor and a validated slot number, never from the
// report's text.
type legendView struct {
	Label string
	Color template.CSS
	Mark  string
}

type videoView struct{ Src, Poster template.URL }

type imageView struct {
	Src                template.URL
	Alt, Caption, Note string
	Wide               bool
}

func serveNight(w http.ResponseWriter, r *http.Request, night string, legacy *legacyReports) {
	rows, err := DB.GetNightReports(r.Context(), night)
	if err != nil {
		slog.Error("Reading night reports", "night", night, "error", err)
		http.Error(w, "could not read reports", http.StatusInternalServerError)
		return
	}
	page := nightPage{
		Night: night,
		Label: nightreport.NightLabel(night),
		PageData: PageData{
			CanonicalURL: "https://deepspaceplace.com/reports/" + night,
			Title:        "Night of " + nightreport.NightLabel(night),
			Description:  "What the observatory recorded on the night of " + nightreport.NightLabel(night) + ": imaging, cloud and the all-sky camera.",
		},
		CSS:    template.CSS(nightreport.CSS + reportSiteCSS),
		Script: template.JS(nightreport.HoverScript),
	}
	var modified time.Time
	for _, row := range rows {
		var rep nightreport.Report
		if err := json.Unmarshal([]byte(row.Body), &rep); err != nil {
			slog.Warn("Stored night report does not decode", "night", night, "source", row.Source, "error", err)
			continue
		}
		if err := rep.Validate(); err != nil {
			slog.Warn("Stored night report does not validate", "night", night, "source", row.Source, "error", err)
			continue
		}
		v := buildReportView(&rep)
		switch rep.Source {
		case nightreport.SourceSkyq:
			page.Imaging = v
		case nightreport.SourceSkycam:
			page.Sky = v
		}
		if t := time.Unix(row.ReceivedAt, 0); t.After(modified) {
			modified = t
		}
	}

	nights := legacy.list()
	ln := nights[night]
	if page.Imaging == nil && ln.Skyq.Exists {
		page.LegacyImaging = "/reports/" + night + ".html"
	}
	if page.Sky == nil && ln.Skycam.Exists {
		page.LegacySky = "/reports/" + legacySkycamDir + "/" + night + ".html"
	}
	if page.Imaging == nil && page.Sky == nil {
		// Nothing re-published yet: the old page is the whole story.
		switch {
		case page.LegacyImaging != "":
			http.Redirect(w, r, page.LegacyImaging, http.StatusFound)
		case page.LegacySky != "":
			http.Redirect(w, r, page.LegacySky, http.StatusFound)
		default:
			http.NotFound(w, r)
		}
		return
	}
	for _, m := range []time.Time{ln.Skyq.ModTime, ln.Skycam.ModTime} {
		if m.After(modified) {
			modified = m
		}
	}

	index, err := reportNights(r, legacy)
	if err == nil {
		for i, n := range index {
			if n.Night != night {
				continue
			}
			// index is newest first: the next night is the one before.
			if i > 0 {
				page.Next = &reportNav{Href: index[i-1].Href, Label: index[i-1].Label}
			}
			if i+1 < len(index) {
				page.Prev = &reportNav{Href: index[i+1].Href, Label: index[i+1].Label}
			}
		}
	}

	serveReportPage(w, r, "report_night.html", page, modified)
}

func buildReportView(rep *nightreport.Report) *reportView {
	anchor := "imaging"
	if rep.Source == nightreport.SourceSkycam {
		anchor = "sky"
	}
	v := &reportView{
		Anchor: anchor, Title: rep.Title, Verdict: rep.Verdict, Stats: rep.Stats, Foot: rep.Foot,
	}
	if !rep.GeneratedAt.IsZero() {
		v.Generated = rep.GeneratedAt.In(reportZone).Format("2 Jan 2006 15:04")
	}
	for i, s := range rep.Sections {
		sv := sectionView{Heading: s.Heading, Notes: s.Notes, Events: s.Events, Table: s.Table}
		for _, l := range s.Legend {
			lv := legendView{Label: l.Label, Mark: l.Mark}
			if l.Mark == "" {
				lv.Color = template.CSS(nightreport.SlotColor(l.Slot))
			}
			sv.Legend = append(sv.Legend, lv)
		}
		if s.Video != nil && allowedReportMedia(s.Video.Src) {
			sv.Video = &videoView{Src: template.URL(s.Video.Src)}
			if allowedReportMedia(s.Video.Poster) {
				sv.Video.Poster = template.URL(s.Video.Poster)
			}
		}
		if s.Chart != nil {
			id := fmt.Sprintf("%s-chart-%d", anchor, i)
			sv.SVG = nightreport.RenderChart(*s.Chart, id)
			sv.Hover = nightreport.HoverJSON(*s.Chart, id)
		}
		for _, im := range s.Images {
			// Checked on the way in too; this keeps a row stored under an
			// older allowlist from reaching the page.
			if !allowedReportMedia(im.Src) {
				continue
			}
			sv.Images = append(sv.Images, imageView{
				Src: template.URL(im.Src), Alt: im.Alt, Caption: im.Caption, Note: im.Note, Wide: im.Wide,
			})
		}
		v.Sections = append(v.Sections, sv)
	}
	return v
}

// serverStarted bounds Last-Modified from below; see serveReportPage.
var serverStarted = time.Now()

// reportZone is where the observatory is; report times are local to it.
var reportZone = func() *time.Location {
	loc, err := time.LoadLocation("Australia/Melbourne")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// ── index ───────────────────────────────────────────────────────────────

type indexPart struct {
	Href    string
	Summary string
	Pills   []string
	Legacy  bool
}

type indexNight struct {
	Night, Label, Href string
	Imaging, Sky       *indexPart
}

type indexMonth struct {
	Label  string
	Nights []indexNight
}

type reportIndexPage struct {
	PageData
	Months []indexMonth
	Count  int
	CSS    template.CSS
}

func serveReportIndex(w http.ResponseWriter, r *http.Request, legacy *legacyReports) {
	nights, err := reportNights(r, legacy)
	if err != nil {
		slog.Error("Listing night reports", "error", err)
		http.Error(w, "could not list reports", http.StatusInternalServerError)
		return
	}
	page := reportIndexPage{
		PageData: PageData{
			CanonicalURL: "https://deepspaceplace.com/reports/",
			Title:        "Night reports",
			Description:  "One report per night from the Deep Space Place observatory: the imaging session's sky transparency and the all-sky camera's cloud, timelapse and keogram.",
		},
		Count: len(nights),
		CSS:   template.CSS(nightreport.CSS + reportSiteCSS),
	}
	for _, n := range nights {
		t, _ := time.Parse("2006-01-02", n.Night)
		month := t.Format("January 2006")
		if len(page.Months) == 0 || page.Months[len(page.Months)-1].Label != month {
			page.Months = append(page.Months, indexMonth{Label: month})
		}
		last := &page.Months[len(page.Months)-1]
		last.Nights = append(last.Nights, n)
	}
	serveReportPage(w, r, "reports.html", page, legacy.newest())
}

// reportNights is every night with a report of either kind, newest first.
func reportNights(r *http.Request, legacy *legacyReports) ([]indexNight, error) {
	rows, err := DB.ListNightReportSummaries(r.Context())
	if err != nil {
		return nil, err
	}
	byNight := map[string]*indexNight{}
	get := func(night string) *indexNight {
		n := byNight[night]
		if n == nil {
			n = &indexNight{Night: night, Label: shortNightLabel(night), Href: "/reports/" + night}
			byNight[night] = n
		}
		return n
	}
	for _, row := range rows {
		part := &indexPart{Href: "/reports/" + row.Night, Summary: clip(row.Summary, 200)}
		_ = json.Unmarshal([]byte(row.Pills), &part.Pills)
		n := get(row.Night)
		switch row.Source {
		case nightreport.SourceSkyq:
			part.Href += "#imaging"
			n.Imaging = part
		case nightreport.SourceSkycam:
			part.Href += "#sky"
			n.Sky = part
		}
	}
	for night, ln := range legacy.list() {
		n := get(night)
		if n.Imaging == nil && ln.Skyq.Exists {
			n.Imaging = &indexPart{Href: "/reports/" + night + ".html", Summary: ln.Skyq.Summary, Legacy: true}
		}
		if n.Sky == nil && ln.Skycam.Exists {
			n.Sky = &indexPart{Href: "/reports/" + legacySkycamDir + "/" + night + ".html", Summary: ln.Skycam.Summary, Legacy: true}
		}
	}
	out := make([]indexNight, 0, len(byNight))
	for _, n := range byNight {
		// A night known only from old files links to one of them directly,
		// rather than to a night page that would only redirect.
		if (n.Imaging == nil || n.Imaging.Legacy) && (n.Sky == nil || n.Sky.Legacy) {
			if n.Imaging != nil {
				n.Href = n.Imaging.Href
			} else if n.Sky != nil {
				n.Href = n.Sky.Href
			}
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Night > out[j].Night })
	return out, nil
}

// shortNightLabel is the index's name for a night: "Tue 3–4 Nov".
func shortNightLabel(night string) string {
	t, err := time.Parse("2006-01-02", night)
	if err != nil {
		return night
	}
	next := t.AddDate(0, 0, 1)
	if t.Month() == next.Month() {
		return fmt.Sprintf("%s %d–%d %s", t.Format("Mon"), t.Day(), next.Day(), t.Format("Jan"))
	}
	return fmt.Sprintf("%s %d %s – %d %s", t.Format("Mon"), t.Day(), t.Format("Jan"), next.Day(), next.Format("Jan"))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// serveReportPage renders a report page into a buffer, then serves it
// no-cache with Last-Modified, so a template failure is a clean 500 and a
// returning browser revalidates for free.
func serveReportPage(w http.ResponseWriter, r *http.Request, name string, data any, modified time.Time) {
	// The page also depends on the templates and the inlined CSS, which
	// change with a deploy rather than with the reports. Without this a
	// returning browser would revalidate, get 304, and keep the old layout.
	if serverStarted.After(modified) {
		modified = serverStarted
	}
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
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "", modified, bytes.NewReader(buf.Bytes()))
}

// reportSiteCSS seats the report's dark palette on the site's surfaces, and
// undoes the site's blanket "span, div, td { color }" rule where the report
// colours text itself.
const reportSiteCSS = `
.nr.nr-dark { --plane: var(--bg-deep); --surface-1: var(--bg-surface); --border: var(--border-medium) }
.nr .legend span, .nr .evrow span:not(.pill) { color: inherit }
.nr .legend { color: var(--text-secondary) }
.nr h2 { margin-top: 2.2rem }
.nr section { scroll-margin-top: 100px }
.nr .nr-nav { display: flex; justify-content: space-between; gap: 12px; font-size: 13px; margin: 0 0 18px; flex-wrap: wrap }
.nr .nr-nav.bottom { margin: 30px 0 0; padding-top: 12px; border-top: 1px solid var(--grid) }
.nr .nr-nav .off { color: var(--muted) }
.nr .nr-old { font-size: 13px; color: var(--text-secondary) }
.nr .night { display: grid; grid-template-columns: 150px 1fr; gap: 4px 16px; align-items: baseline; padding: 12px 0; border-top: 1px solid var(--grid) }
.nr .night:first-child { border-top: 0 }
.nr .night > a { font-weight: 600; font-variant-numeric: tabular-nums; text-decoration: none }
.nr .night .part { font-size: 13px; color: var(--text-secondary); margin-bottom: 4px; max-width: 80ch }
.nr .night .part a { font-weight: 600; margin-right: 6px }
.nr .night .pills { display: inline-flex; gap: 6px; flex-wrap: wrap; margin-left: 6px }
@media (max-width: 560px) { .nr .night { grid-template-columns: 1fr } }
`

// ── legacy HTML ─────────────────────────────────────────────────────────

// legacyReports reads the HTML the producers published before the API.
type legacyReports struct {
	dir   string
	mu    sync.Mutex
	cache map[string]legacySummary // by relative path
}

type legacySummary struct {
	ModTime time.Time
	Summary string
}

type legacyFile struct {
	Exists  bool
	ModTime time.Time
	Summary string
}

type legacyNight struct {
	Skyq, Skycam legacyFile
}

// list finds the legacy pages on disk, by night. Summaries are read once
// per file version and cached.
func (l *legacyReports) list() map[string]legacyNight {
	out := map[string]legacyNight{}
	for _, sub := range []string{"", legacySkycamDir} {
		entries, err := os.ReadDir(filepath.Join(l.dir, sub))
		if err != nil {
			continue
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".html") || !nightPattern.MatchString(strings.TrimSuffix(name, ".html")) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			rel := path.Join(sub, name)
			f := legacyFile{Exists: true, ModTime: info.ModTime(), Summary: l.summary(rel, info.ModTime())}
			night := strings.TrimSuffix(name, ".html")
			n := out[night]
			if sub == "" {
				n.Skyq = f
			} else {
				n.Skycam = f
			}
			out[night] = n
		}
	}
	return out
}

func (l *legacyReports) newest() time.Time {
	var t time.Time
	for _, n := range l.list() {
		for _, m := range []time.Time{n.Skyq.ModTime, n.Skycam.ModTime} {
			if m.After(t) {
				t = m
			}
		}
	}
	return t
}

var verdictPattern = regexp.MustCompile(`(?s)<p class="sub">(.*?)</p>`)
var tagPattern = regexp.MustCompile(`<[^>]*>`)

// summary is a legacy page's verdict paragraph, as plain text.
func (l *legacyReports) summary(rel string, mod time.Time) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.cache[rel]; ok && c.ModTime.Equal(mod) {
		return c.Summary
	}
	s := ""
	if b, err := os.ReadFile(filepath.Join(l.dir, filepath.FromSlash(rel))); err == nil {
		if m := verdictPattern.FindSubmatch(b); m != nil {
			s = clip(strings.Join(strings.Fields(html.UnescapeString(tagPattern.ReplaceAllString(string(m[1]), ""))), " "), 200)
		}
	}
	l.cache[rel] = legacySummary{ModTime: mod, Summary: s}
	return s
}

// serve sends a legacy page with the site header injected.
func (l *legacyReports) serve(w http.ResponseWriter, r *http.Request, rel string) {
	// http.Dir cleans the path and joins it under dir, so "../" cannot
	// escape the reports directory.
	f, err := http.Dir(l.dir).Open("/" + rel)
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
}

// siteNav is the header injected at the top of every legacy report page.
//
// The old reports do not load the site's stylesheets, and pulling Bootstrap
// into them would restyle every table and heading. The header is therefore
// styled inline from the report's own CSS variables (surface, border, text
// colours), so it follows the report's light and dark palettes; the
// fallbacks are the report's light values, for a page that defines none of
// them. The class prefix keeps it clear of anything the report styles.
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
