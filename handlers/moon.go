package handlers

import (
	"fmt"
	"net/http"
	"time"
	_ "time/tzdata" // production binary is CGO_ENABLED=0; don't depend on system zoneinfo

	"github.com/exploded/riseset"
)

// moonDays is the number of days shown from the selected start date.
const moonDays = 30

// MoonLocation is a selectable observing site. Lat and Lon are degrees,
// north and east positive, matching riseset's convention.
type MoonLocation struct {
	Key  string
	Name string
	Lat  float64
	Lon  float64
	TZ   string // IANA zone name
	loc  *time.Location
}

// moonLocations is the dropdown, in display order. Melbourne is the default.
var moonLocations = []MoonLocation{
	{Key: "melbourne", Name: "Melbourne, Australia", Lat: -37.8136, Lon: 144.9631, TZ: "Australia/Melbourne"},
	{Key: "sydney", Name: "Sydney, Australia", Lat: -33.8688, Lon: 151.2093, TZ: "Australia/Sydney"},
	{Key: "brisbane", Name: "Brisbane, Australia", Lat: -27.4698, Lon: 153.0251, TZ: "Australia/Brisbane"},
	{Key: "perth", Name: "Perth, Australia", Lat: -31.9505, Lon: 115.8605, TZ: "Australia/Perth"},
	{Key: "adelaide", Name: "Adelaide, Australia", Lat: -34.9285, Lon: 138.6007, TZ: "Australia/Adelaide"},
	{Key: "hobart", Name: "Hobart, Australia", Lat: -42.8821, Lon: 147.3272, TZ: "Australia/Hobart"},
	{Key: "darwin", Name: "Darwin, Australia", Lat: -12.4634, Lon: 130.8456, TZ: "Australia/Darwin"},
	{Key: "canberra", Name: "Canberra, Australia", Lat: -35.2809, Lon: 149.1300, TZ: "Australia/Sydney"},
	{Key: "auckland", Name: "Auckland, New Zealand", Lat: -36.8485, Lon: 174.7633, TZ: "Pacific/Auckland"},
	{Key: "singapore", Name: "Singapore", Lat: 1.3521, Lon: 103.8198, TZ: "Asia/Singapore"},
	{Key: "tokyo", Name: "Tokyo, Japan", Lat: 35.6762, Lon: 139.6503, TZ: "Asia/Tokyo"},
	{Key: "london", Name: "London, United Kingdom", Lat: 51.5074, Lon: -0.1278, TZ: "Europe/London"},
	{Key: "paris", Name: "Paris, France", Lat: 48.8566, Lon: 2.3522, TZ: "Europe/Paris"},
	{Key: "new-york", Name: "New York, USA", Lat: 40.7128, Lon: -74.0060, TZ: "America/New_York"},
	{Key: "los-angeles", Name: "Los Angeles, USA", Lat: 34.0522, Lon: -118.2437, TZ: "America/Los_Angeles"},
	{Key: "santiago", Name: "Santiago, Chile", Lat: -33.4489, Lon: -70.6693, TZ: "America/Santiago"},
	{Key: "cape-town", Name: "Cape Town, South Africa", Lat: -33.9249, Lon: 18.4241, TZ: "Africa/Johannesburg"},
}

var moonLocationByKey = map[string]*MoonLocation{}

func init() {
	for i := range moonLocations {
		l := &moonLocations[i]
		loc, err := time.LoadLocation(l.TZ)
		if err != nil {
			// A typo in the table should fail at boot, not per request.
			panic(fmt.Sprintf("moon location %q: %v", l.Key, err))
		}
		l.loc = loc
		moonLocationByKey[l.Key] = l
	}
}

type MoonDay struct {
	Weekday string
	Date    string
	Zone    string // zone abbreviation, e.g. AEST/AEDT
	Rise    string
	Set     string
}

type MoonData struct {
	CanonicalURL string
	Title        string
	Description  string
	Date         string // start date, yyyy-mm-dd, for the date input
	Location     MoonLocation
	Locations    []MoonLocation
	Days         []MoonDay
}

// HandleMoon renders a 30-day moon rise/set table for the selected location
// and start date. Bad input falls back to the defaults rather than erroring:
// htmx won't swap a non-2xx response, so a 400 would look like a dead form.
func HandleMoon(w http.ResponseWriter, r *http.Request) {
	location := moonLocationByKey["melbourne"]
	if l, ok := moonLocationByKey[r.URL.Query().Get("loc")]; ok {
		location = l
	}

	start, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), location.loc)
	if err != nil || start.Year() < 1900 || start.Year() > 2100 {
		now := time.Now().In(location.loc)
		start = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location.loc)
	}

	data := computeMoonData(location, start)

	w.Header().Set("Vary", "HX-Request")
	if r.Header.Get("HX-Request") == "true" {
		RenderPartial(w, "moon.html", "moon_table.html", data)
		return
	}
	Render(w, "moon.html", data)
}

func computeMoonData(location *MoonLocation, start time.Time) MoonData {
	days := make([]MoonDay, 0, moonDays)
	for i := 0; i < moonDays; i++ {
		// Noon sits clear of the transition hour on daylight-saving change
		// days, so the offset is the one that applies to most of the day.
		d := start.AddDate(0, 0, i)
		date := time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, location.loc)
		zone, offset := date.Zone()

		rs := riseset.Riseset(riseset.Moon, date, location.Lon, location.Lat, float64(offset)/3600)

		day := MoonDay{
			Weekday: date.Format("Mon"),
			Date:    date.Format("02-01-2006"),
			Zone:    zone,
		}

		if rs.AlwaysAbove {
			day.Rise = "****"
			day.Set = "****"
		} else if rs.AlwaysBelow {
			day.Rise = "...."
			day.Set = "...."
		} else {
			day.Rise = rs.Rise
			day.Set = rs.Set
		}

		days = append(days, day)
	}

	return MoonData{
		CanonicalURL: "https://deepspaceplace.com/moon",
		Title:        "Moon Rise & Set",
		Description:  fmt.Sprintf("%d-day moon rise and set forecast for %s.", moonDays, location.Name),
		Date:         start.Format("2006-01-02"),
		Location:     *location,
		Locations:    moonLocations,
		Days:         days,
	}
}
