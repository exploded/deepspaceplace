package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path"
	"regexp"
	"strings"
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
// Pages go out no-cache with Last-Modified: a night can be re-published at
// any time, and the 7-day immutable cache on /static/ would pin stale pages.
func Reports() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		switch {
		case p == "":
			serveReportIndex(w, r)
		case nightPattern.MatchString(p):
			serveNight(w, r, p)
		default:
			http.NotFound(w, r)
		}
	})
}

var nightPattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ── night page ──────────────────────────────────────────────────────────

type reportNav struct {
	Href, Label string
}

type nightPage struct {
	PageData
	Night, Label string
	Prev, Next   *reportNav
	Imaging, Sky *reportView
	CSS          template.CSS
	Script       template.JS
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

func serveNight(w http.ResponseWriter, r *http.Request, night string) {
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

	if page.Imaging == nil && page.Sky == nil {
		http.NotFound(w, r)
		return
	}

	index, _, err := reportNights(r)
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

func serveReportIndex(w http.ResponseWriter, r *http.Request) {
	nights, modified, err := reportNights(r)
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
	serveReportPage(w, r, "reports.html", page, modified)
}

// reportNights is every night with a report of either kind, newest first,
// and when the newest of them arrived.
func reportNights(r *http.Request) ([]indexNight, time.Time, error) {
	rows, err := DB.ListNightReportSummaries(r.Context())
	if err != nil {
		return nil, time.Time{}, err
	}
	var modified time.Time
	var out []indexNight
	for _, row := range rows {
		// rows come newest night first, sources together within a night.
		if len(out) == 0 || out[len(out)-1].Night != row.Night {
			out = append(out, indexNight{Night: row.Night, Label: shortNightLabel(row.Night), Href: "/reports/" + row.Night})
		}
		n := &out[len(out)-1]
		part := &indexPart{Href: "/reports/" + row.Night, Summary: clip(row.Summary, 200)}
		_ = json.Unmarshal([]byte(row.Pills), &part.Pills)
		switch row.Source {
		case nightreport.SourceSkyq:
			part.Href += "#imaging"
			n.Imaging = part
		case nightreport.SourceSkycam:
			part.Href += "#sky"
			n.Sky = part
		}
		if t := time.Unix(row.ReceivedAt, 0); t.After(modified) {
			modified = t
		}
	}
	return out, modified, nil
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
.nr .night { display: grid; grid-template-columns: 150px 1fr; gap: 4px 16px; align-items: baseline; padding: 12px 0; border-top: 1px solid var(--grid) }
.nr .night:first-child { border-top: 0 }
.nr .night > a { font-weight: 600; font-variant-numeric: tabular-nums; text-decoration: none }
.nr .night .part { font-size: 13px; color: var(--text-secondary); margin-bottom: 4px; max-width: 80ch }
.nr .night .part a { font-weight: 600; margin-right: 6px }
.nr .night .pills { display: inline-flex; gap: 6px; flex-wrap: wrap; margin-left: 6px }
@media (max-width: 560px) { .nr .night { grid-template-columns: 1fr } }
`
