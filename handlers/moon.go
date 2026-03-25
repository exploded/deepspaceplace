package handlers

import (
	"net/http"
	"sync"
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

var (
	moonCache     MoonData
	moonCacheDate string
	moonCacheMu   sync.Mutex
)

func HandleMoon(w http.ResponseWriter, r *http.Request) {
	today := time.Now().Format("2006-01-02")

	moonCacheMu.Lock()
	if moonCacheDate == today {
		data := moonCache
		moonCacheMu.Unlock()
		Render(w, "moon.html", data)
		return
	}
	moonCacheMu.Unlock()

	data := computeMoonData()

	moonCacheMu.Lock()
	moonCache = data
	moonCacheDate = today
	moonCacheMu.Unlock()

	Render(w, "moon.html", data)
}

func computeMoonData() MoonData {
	lon := 144.9
	lat := -37.8
	zon := 10.0

	now := time.Now()
	numDays := 60

	days := make([]MoonDay, 0, numDays)
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

	return MoonData{Days: days}
}
