package handlers

import (
	"net/http"
	"time"

	"github.com/exploded/riseset"
)

type MoonDay struct {
	Weekday string
	Date    string
	Rise    string
	Set     string
}

type MoonData struct {
	Days []MoonDay
}

func HandleMoon(w http.ResponseWriter, r *http.Request) {
	lon := 144.9
	lat := -37.8
	zon := 10.0

	now := time.Now()
	numDays := 60

	var days []MoonDay
	for i := 0; i < numDays; i++ {
		date := now.AddDate(0, 0, i)
		rs := riseset.Riseset(riseset.Moon, date, lon, lat, zon)

		day := MoonDay{
			Weekday: date.Format("Mon"),
			Date:    date.Format("02-01-2006"),
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

	data := MoonData{Days: days}

	Render(w, "moon.html", data)
}
