package handlers

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

var bomClient = &http.Client{
	Timeout: 10 * time.Second,
}

// bomFrameAttempts is how many half-hourly frames back the proxy will look for
// a published image before giving up.
const bomFrameAttempts = 6

func HandleWeather(w http.ResponseWriter, r *http.Request) {
	Render(w, "weather.html", PageData{
		CanonicalURL: "https://deepspaceplace.com/weather",
		Title:        "Weather",
		Description:  "Australian Bureau of Meteorology satellite imagery for astrophotography planning.",
	})
}

func HandleBOMProxy(w http.ResponseWriter, r *http.Request) {
	imageType := r.URL.Query().Get("type")
	if imageType == "" {
		imageType = "national"
	}

	imageCodes := map[string]string{
		"national":  "IDE00105",
		"composite": "IDE00135",
		"victoria":  "IDE00107",
	}

	imageCode, ok := imageCodes[imageType]
	if !ok {
		imageCode = imageCodes["national"]
	}

	now := time.Now().UTC()

	// BOM publishes each frame several minutes after its nominal timestamp, so
	// the newest half-hour slot is normally still missing and the one before it
	// serves. Walking back until a frame exists is the expected path, not a
	// fault: misses log at DEBUG, and only running out of frames is worth a
	// warning.
	var lastErr error
	lastStatus := 0

	for i := 0; i < bomFrameAttempts; i++ {
		ts := now.Add(time.Duration(-i*30) * time.Minute)
		minute := (ts.Minute() / 30) * 30
		ts = time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), minute, 0, 0, time.UTC)

		url := fmt.Sprintf("https://www.bom.gov.au/gms/%s.%s.jpg", imageCode, ts.Format("200601021504"))

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		resp, err := bomClient.Do(req)
		if err != nil {
			lastErr = err
			slog.Debug("BOM frame fetch failed, trying older frame", "url", url, "error", err)
			continue
		}

		if resp.StatusCode == 200 {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "max-age=1800")
			if _, err := io.Copy(w, resp.Body); err != nil {
				slog.Error("BOM proxy write error", "error", err)
			}
			resp.Body.Close()
			return
		}
		lastStatus = resp.StatusCode
		slog.Debug("BOM frame not published yet, trying older frame", "url", url, "status", resp.StatusCode)
		resp.Body.Close()
	}

	// Every frame in the window was missing -- BOM is genuinely unreachable or
	// has changed its URL scheme, and the weather page is showing broken images.
	slog.Warn("BOM satellite unavailable, no published frame in window",
		"type", imageType, "attempts", bomFrameAttempts,
		"last_status", lastStatus, "last_error", lastErr)
	http.Error(w, "Failed to fetch BOM satellite image", http.StatusBadGateway)
}
