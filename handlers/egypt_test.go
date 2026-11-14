package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const egyptDir = "../templates/egypt"

func TestEgyptPageRendersContent(t *testing.T) {
	page, err := renderEgypt(egyptDir)
	if err != nil {
		t.Fatal(err)
	}
	html := string(page)
	data, err := loadEgyptData(filepath.Join(egyptDir, "trip.json"))
	if err != nil {
		t.Fatal(err)
	}

	// html/template writes ZgotmplZ when it rejects a value in a URL or CSS context.
	if strings.Contains(html, "ZgotmplZ") {
		t.Error("page contains ZgotmplZ: a value was rejected by html/template")
	}
	counts := map[string]int{
		`class="day-card" id="day-`: len(data.Trip),
		`<a href="#day-`:            len(data.Trip),
		`class="guide-card"`:        len(data.Guides),
		`class="gallery-cell"`:      len(data.Gallery),
	}
	for s, want := range counts {
		if got := strings.Count(html, s); got != want {
			t.Errorf("%q appears %d times, want %d", s, got, want)
		}
	}

	// Every day's text and every caption reaches the HTML, not just the script.
	for _, d := range data.Trip {
		if !strings.Contains(html, "<div class=\"body-text\">"+string(d.Body)+"</div>") {
			t.Errorf("day %d body not rendered as HTML", d.Day)
		}
	}
	for _, want := range []string{
		"double-walled calcite lamp",
		"Queen&#39;s Chamber",
		`<a href="https://en.wikipedia.org/wiki/Sabu_disk"`,
		`<meta property="og:image" content="https://deepspaceplace.com/egypt/photos/`,
		`style="background-image:url(photos/thumbs/day02-giza-khafre-camels.jpg)"`,
		`style="object-position:center 40%"`,
		`style="grid-column:span 2;grid-row:span 1"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// The embedded data parses back to the same trip.
	m := regexp.MustCompile(`(?s)<script type="application/json" id="egypt-data">(.*?)</script>`).FindStringSubmatch(html)
	if m == nil {
		t.Fatal("no egypt-data script block")
	}
	var round egyptData
	if err := json.Unmarshal([]byte(m[1]), &round); err != nil {
		t.Fatalf("egypt-data is not valid JSON: %v", err)
	}
	if len(round.Trip) != len(data.Trip) || round.Trip[2].Body != data.Trip[2].Body {
		t.Error("egypt-data does not match trip.json")
	}

	// Thumb buttons carry the viewer's indexes 0..n-1 for each day with a strip.
	want := 0
	for _, d := range data.Trip {
		n := len(d.Photos)
		for _, g := range d.Groups {
			n += len(g.Photos)
		}
		if n > 1 {
			want += n
		}
	}
	if got := strings.Count(html, `data-idx="`); got != want {
		t.Errorf("%d thumb buttons, want %d", got, want)
	}
}

// TestEgyptMediaExists checks every photo, video and thumbnail the page
// references. The media folder is gitignored, so this runs only where it exists.
func TestEgyptMediaExists(t *testing.T) {
	root := "../static/egypt"
	if _, err := os.Stat(filepath.Join(root, "photos")); err != nil {
		t.Skip("static/egypt/photos not present (gitignored)")
	}
	page, err := renderEgypt(egyptDir)
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`photos/[A-Za-z0-9/_-]+\.(?:jpg|mp4)`).FindAllString(string(page), -1)
	seen := map[string]bool{}
	for _, ref := range refs {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		if _, err := os.Stat(filepath.Join(root, ref)); err != nil {
			t.Errorf("missing media: %s", ref)
		}
	}
	if len(seen) < 100 {
		t.Errorf("only %d media references found; the pattern may be wrong", len(seen))
	}
}

func TestEgyptPageCaching(t *testing.T) {
	if err := LoadEgypt(egyptDir); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	EgyptPage(rec, httptest.NewRequest("GET", "/egypt/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type %q", ct)
	}
	lm := rec.Header().Get("Last-Modified")
	if lm == "" || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("want no-cache with Last-Modified, got %v", rec.Header())
	}

	req := httptest.NewRequest("GET", "/egypt/", nil)
	req.Header.Set("If-Modified-Since", lm)
	rec = httptest.NewRecorder()
	EgyptPage(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("revalidation: status %d, want 304", rec.Code)
	}
}

func TestFirstSentence(t *testing.T) {
	for in, want := range map[string]string{
		"Oriented to true north within 0.05°. Its base is level.": "Oriented to true north within 0.05°",
		"A single sentence.": "A single sentence",
		"No full stop":       "No full stop",
	} {
		if got := firstSentence(in); got != want {
			t.Errorf("firstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}
