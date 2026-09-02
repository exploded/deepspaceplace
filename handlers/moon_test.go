package handlers

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/exploded/riseset"
)

// moonTestTemplates swaps in a stub template set that renders just enough
// of the page to assert on: the selected location, the start date and the
// table rows. The "base" wrapper lets the test tell a full page from a partial.
func moonTestTemplates(t *testing.T) {
	t.Helper()
	oldTemplates := Templates
	Templates = map[string]*template.Template{
		"moon.html": template.Must(template.New("moon.html").Parse(
			`{{define "base"}}<html><form>{{.Location.Key}} {{.Date}}</form>{{template "moon_table.html" .}}</html>{{end}}` +
				`{{define "moon_table.html"}}{{range .Days}}<tr>{{.Date}}|{{.Zone}}|{{.Rise}}|{{.Set}}</tr>{{end}}{{end}}`)),
	}
	t.Cleanup(func() { Templates = oldTemplates })
}

func getMoon(t *testing.T, url string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	HandleMoon(w, req)
	return w
}

// TestMoonDefaults: no parameters means Melbourne and today's date in
// Melbourne (not the server's zone), with moonDays rows.
func TestMoonDefaults(t *testing.T) {
	moonTestTemplates(t)
	w := getMoon(t, "https://deepspaceplace.com/moon", false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()

	melb, _ := time.LoadLocation("Australia/Melbourne")
	today := time.Now().In(melb)
	if want := "<form>melbourne " + today.Format("2006-01-02") + "</form>"; !strings.Contains(body, want) {
		t.Errorf("body missing %q:\n%s", want, body)
	}
	if got := strings.Count(body, "<tr>"); got != moonDays {
		t.Errorf("rows = %d, want %d", got, moonDays)
	}
	if !strings.Contains(body, "<tr>"+today.Format("02-01-2006")) {
		t.Errorf("first row should be today (%s):\n%s", today.Format("02-01-2006"), body)
	}
}

// TestMoonParams: the date and loc query parameters select the start date
// and location, and the zone column reflects daylight saving (Sydney in
// January is AEDT).
func TestMoonParams(t *testing.T) {
	moonTestTemplates(t)
	w := getMoon(t, "https://deepspaceplace.com/moon?date=2026-01-10&loc=sydney", false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "<form>sydney 2026-01-10</form>") {
		t.Errorf("location/date not selected:\n%s", body)
	}
	if !strings.Contains(body, "<tr>10-01-2026|AEDT|") {
		t.Errorf("first row should be 10-01-2026 in AEDT:\n%s", body)
	}
}

// TestMoonDaylightSaving: the old page used a fixed UTC+10 for Melbourne all
// year, an hour out in summer. The times must now match riseset at +11 in
// January.
func TestMoonDaylightSaving(t *testing.T) {
	moonTestTemplates(t)
	w := getMoon(t, "https://deepspaceplace.com/moon?date=2026-01-10&loc=melbourne", false)
	body := w.Body.String()

	d := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	want := riseset.Riseset(riseset.Moon, d, 144.9631, -37.8136, 11)
	wrong := riseset.Riseset(riseset.Moon, d, 144.9631, -37.8136, 10)
	if want.Rise == wrong.Rise {
		t.Skip("rise time happens to be identical at +10 and +11; pick another date")
	}
	row := "<tr>10-01-2026|AEDT|" + want.Rise + "|" + want.Set + "</tr>"
	if !strings.Contains(body, row) {
		t.Errorf("body missing %q:\n%s", row, body)
	}
}

// TestMoonBadInput: unknown location and unparseable date fall back to the
// defaults with a 200, because htmx won't swap a non-2xx response.
func TestMoonBadInput(t *testing.T) {
	moonTestTemplates(t)
	w := getMoon(t, "https://deepspaceplace.com/moon?date=nope&loc=atlantis", false)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	melb, _ := time.LoadLocation("Australia/Melbourne")
	want := "<form>melbourne " + time.Now().In(melb).Format("2006-01-02") + "</form>"
	if !strings.Contains(w.Body.String(), want) {
		t.Errorf("body missing %q:\n%s", want, w.Body.String())
	}
}

// TestMoonPartial: an htmx request gets only the table, not the page shell.
func TestMoonPartial(t *testing.T) {
	moonTestTemplates(t)
	w := getMoon(t, "https://deepspaceplace.com/moon?loc=perth", true)
	body := w.Body.String()
	if strings.Contains(body, "<html>") || strings.Contains(body, "<form>") {
		t.Errorf("partial response contains page shell:\n%s", body)
	}
	if got := strings.Count(body, "<tr>"); got != moonDays {
		t.Errorf("rows = %d, want %d", got, moonDays)
	}
	if w.Header().Get("Vary") != "HX-Request, HX-Request-Type" {
		t.Errorf("Vary = %q, want HX-Request, HX-Request-Type", w.Header().Get("Vary"))
	}
}

// TestMoonHistoryRestore: htmx 4 re-fetches the pushed URL on back/forward
// navigation with HX-Request set but HX-Request-Type "full". That request
// must get the whole page, not the bare table, or the restored page is empty
// apart from the rows.
func TestMoonHistoryRestore(t *testing.T) {
	moonTestTemplates(t)
	req := httptest.NewRequest("GET", "https://deepspaceplace.com/moon?loc=perth", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Request-Type", "full")
	w := httptest.NewRecorder()
	HandleMoon(w, req)
	if !strings.Contains(w.Body.String(), "<form") {
		t.Errorf("history-restore response missing page shell:\n%s", w.Body.String())
	}
}

// TestMoonLocationsLoad: every entry in the dropdown has a loadable zone,
// a unique key, and a plausible coordinate. init() already panics on a bad
// zone, but this gives a readable failure.
func TestMoonLocationsLoad(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range moonLocations {
		if seen[l.Key] {
			t.Errorf("duplicate key %q", l.Key)
		}
		seen[l.Key] = true
		if _, err := time.LoadLocation(l.TZ); err != nil {
			t.Errorf("%s: %v", l.Key, err)
		}
		if l.Lat < -90 || l.Lat > 90 || l.Lon < -180 || l.Lon > 180 {
			t.Errorf("%s: coordinates out of range", l.Key)
		}
	}
	if moonLocations[0].Key != "melbourne" {
		t.Errorf("first location should be the default (melbourne), got %q", moonLocations[0].Key)
	}
}
