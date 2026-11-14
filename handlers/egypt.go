package handlers

// The Egypt field record is one standalone page at /egypt/. Its content lives
// in templates/egypt/trip.json and its layout in templates/egypt/index.html.
// The server renders every day, site and video plate, so the itinerary reaches
// search engines, link previews and readers without JavaScript. The page's
// script then adds the map, photo viewer and lightbox, reading the same data
// from a JSON block embedded in the page.
//
// The content changes only on deploy, so the page is rendered once at startup
// and served from memory.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func HandleEgypt(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/egypt/", http.StatusMovedPermanently)
}

// egyptDescription is the page's meta and link-preview description.
const egyptDescription = "Fourteen days tracing the Nile from Giza to Abu Simbel — in search of what the old stones remember, beneath the same sky that watched them rise."

// trip.json schema. The JSON keys are the names the page's script uses.
// Body and Caption may hold links, so they are trusted HTML; every other
// field is plain text.
type egyptPhoto struct {
	Src     string        `json:"src"`
	Caption template.HTML `json:"caption"`
	Crop    string        `json:"crop,omitempty"`
	Video   bool          `json:"video,omitempty"`
}

type egyptGroup struct {
	Label  string       `json:"label"`
	Photos []egyptPhoto `json:"photos"`
}

type egyptSubPin struct {
	Label string `json:"label"`
	DY    int    `json:"dy"`
}

type egyptDay struct {
	Day      int           `json:"day"`
	Date     string        `json:"date"`
	Place    string        `json:"place"`
	Region   string        `json:"region"`
	Glyph    string        `json:"glyph"`
	Hiero    string        `json:"hiero"`
	Meaning  string        `json:"meaning"`
	Coord    string        `json:"coord"`
	Lede     string        `json:"lede"`
	Body     template.HTML `json:"body"`
	Stops    []string      `json:"stops"`
	MapLabel string        `json:"mapLabel,omitempty"`
	SubPins  []egyptSubPin `json:"subPins,omitempty"`
	Photos   []egyptPhoto  `json:"photos,omitempty"`
	Groups   []egyptGroup  `json:"groups,omitempty"`
}

type egyptGuide struct {
	Name        string `json:"name"`
	Sub         string `json:"sub"`
	Glyph       string `json:"glyph"`
	Metric      string `json:"metric"`
	MetricLabel string `json:"metricLabel"`
	Detail      string `json:"detail"`
	Quote       string `json:"quote"`
}

type egyptPlate struct {
	Mode  string `json:"mode"`
	Label string `json:"label"`
	Day   int    `json:"day"`
	Size  string `json:"size"` // grid span, "<columns>x<rows>"
	Video string `json:"video,omitempty"`
}

type egyptData struct {
	Trip    []egyptDay   `json:"trip"`
	Guides  []egyptGuide `json:"guides"`
	Gallery []egyptPlate `json:"gallery"`
}

// View models: everything the template needs, worked out in Go so the
// template stays declarative.
type egyptThumb struct {
	Idx    int
	Thumb  string
	Label  template.HTML // caption; html/template strips tags in attributes
	Video  bool
	Active bool
}

type egyptThumbGroup struct {
	Label  string
	Active bool
	Thumbs []egyptThumb
}

type egyptDayView struct {
	egyptDay
	Num         string // "07"
	PlaceUpper  string
	MapKicker   string // "Day 01 · Wed"
	Hero        *egyptPhoto
	PlateNum    string // "PLATE 07 · 01 / 04"
	ThumbGroups []egyptThumbGroup
	Thumbs      []egyptThumb // ungrouped days with more than one photo
	ArtMode     string       // days with no photos
}

type egyptGuideView struct {
	egyptGuide
	Tip string
}

type egyptPlateView struct {
	egyptPlate
	Cols, Rows int
	DayNum     string
}

type egyptPageView struct {
	Description string
	First       egyptDayView
	Days        []egyptDayView
	Guides      []egyptGuideView
	Plates      []egyptPlateView
	DataJSON    template.JS
}

var (
	egyptHTML     []byte
	egyptModified time.Time
)

// LoadEgypt reads trip.json and index.html from dir and renders the page.
// Call it once at startup; EgyptPage serves the result.
func LoadEgypt(dir string) error {
	page, err := renderEgypt(dir)
	if err != nil {
		return err
	}
	egyptHTML = page
	egyptModified = time.Now()
	return nil
}

// EgyptPage serves the page rendered by LoadEgypt. no-cache makes browsers
// revalidate, and Last-Modified (server start) lets them get a 304 until the
// next deploy.
func EgyptPage(w http.ResponseWriter, r *http.Request) {
	if egyptHTML == nil {
		http.Error(w, "Egypt page not loaded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", egyptModified, bytes.NewReader(egyptHTML))
}

func renderEgypt(dir string) ([]byte, error) {
	data, err := loadEgyptData(filepath.Join(dir, "trip.json"))
	if err != nil {
		return nil, err
	}
	view, err := buildEgyptView(data)
	if err != nil {
		return nil, err
	}
	funcs := template.FuncMap{"num2": num2, "inc": func(i int) int { return i + 1 }}
	tmpl, err := template.New("index.html").Funcs(funcs).ParseFiles(filepath.Join(dir, "index.html"))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", view); err != nil {
		return nil, fmt.Errorf("rendering Egypt page: %w", err)
	}
	return buf.Bytes(), nil
}

func loadEgyptData(path string) (*egyptData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields() // catch misspelt keys in hand-edited JSON
	var d egyptData
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(d.Trip) == 0 {
		return nil, fmt.Errorf("%s: no days", path)
	}
	return &d, nil
}

// egyptArtModes are the drawn plates shown for a day that has no photos.
var egyptArtModes = []string{"plateau", "pyramid", "museum", "temple", "hypostyle", "valley", "columns", "river", "island", "obelisk", "serapeum", "altar", "chamber"}

func buildEgyptView(d *egyptData) (*egyptPageView, error) {
	js, err := json.Marshal(d) // escapes <, > and &, so it can't close the script element
	if err != nil {
		return nil, err
	}
	v := &egyptPageView{Description: egyptDescription, DataJSON: template.JS(js)}

	for i, day := range d.Trip {
		dv := egyptDayView{
			egyptDay:   day,
			Num:        num2(day.Day),
			PlaceUpper: strings.ToUpper(day.Place),
			MapKicker:  "Day " + num2(day.Day) + " · " + strings.SplitN(day.Date, " · ", 2)[0],
		}
		// Flatten the photos in the order the script's viewer uses, so a
		// thumb's data-idx points at the same photo on both sides.
		var flat []egyptPhoto
		for _, g := range day.Groups {
			tg := egyptThumbGroup{Label: g.Label, Active: len(flat) == 0}
			for _, p := range g.Photos {
				tg.Thumbs = append(tg.Thumbs, newEgyptThumb(len(flat), p))
				flat = append(flat, p)
			}
			dv.ThumbGroups = append(dv.ThumbGroups, tg)
		}
		if len(day.Groups) == 0 {
			flat = day.Photos
			if len(flat) > 1 {
				for j, p := range flat {
					dv.Thumbs = append(dv.Thumbs, newEgyptThumb(j, p))
				}
			}
		}
		for _, p := range flat {
			if p.Src == "" {
				return nil, fmt.Errorf("day %d: photo with no src", day.Day)
			}
		}
		if len(flat) > 0 {
			hero := flat[0]
			if hero.Crop == "" {
				hero.Crop = "center"
			}
			dv.Hero = &hero
			dv.PlateNum = "PLATE " + num2(day.Day) + " · 01 / " + num2(len(flat))
		} else {
			dv.ArtMode = egyptArtModes[i%len(egyptArtModes)]
		}
		v.Days = append(v.Days, dv)
	}
	v.First = v.Days[0]

	for _, g := range d.Guides {
		v.Guides = append(v.Guides, egyptGuideView{egyptGuide: g, Tip: firstSentence(g.Detail)})
	}

	for _, p := range d.Gallery {
		cols, rows, err := parseSpan(p.Size)
		if err != nil {
			return nil, fmt.Errorf("gallery plate %q: %w", p.Label, err)
		}
		v.Plates = append(v.Plates, egyptPlateView{egyptPlate: p, Cols: cols, Rows: rows, DayNum: num2(p.Day)})
	}
	return v, nil
}

func newEgyptThumb(idx int, p egyptPhoto) egyptThumb {
	thumb := strings.Replace(p.Src, "photos/", "photos/thumbs/", 1)
	if p.Video {
		thumb = strings.TrimSuffix(thumb, ".mp4") + ".jpg"
	}
	return egyptThumb{Idx: idx, Thumb: thumb, Label: p.Caption, Video: p.Video, Active: idx == 0}
}

func num2(n int) string { return fmt.Sprintf("%02d", n) }

// firstSentence returns s up to its first full stop followed by a space, so
// a decimal such as "0.05°" doesn't end the sentence.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i]
	}
	return strings.TrimSuffix(s, ".")
}

// parseSpan parses a grid span such as "2x1".
func parseSpan(s string) (cols, rows int, err error) {
	c, r, ok := strings.Cut(s, "x")
	if !ok {
		return 0, 0, errors.New("size must look like 2x1")
	}
	if cols, err = strconv.Atoi(c); err != nil || cols < 1 {
		return 0, 0, errors.New("bad column span in size")
	}
	if rows, err = strconv.Atoi(r); err != nil || rows < 1 {
		return 0, 0, errors.New("bad row span in size")
	}
	return cols, rows, nil
}
