package handlers

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"deepspaceplace/internal/database"

	"github.com/exploded/nightreport"
)

// ReportKeys holds each producer's API key, by source. A source with no key
// cannot publish. Set from REPORT_KEY_SKYQ and REPORT_KEY_SKYCAM.
var ReportKeys = map[string]string{}

// maxReportBody caps one PUT. A skyq night with its all-sky thumbnails is a
// few hundred KB; skycam's is smaller because its images are S3 links.
const maxReportBody = 4 << 20

// reportUploadTimeout replaces the server's 5-second read timeout for an
// upload, which a few hundred KB from the observatory's link can exceed.
const reportUploadTimeout = 60 * time.Second

// earliestReportNight bounds the nights a producer may write. With the
// one-day lead on the other end, a stolen key can write a bounded number of
// rows rather than one per possible date.
var earliestReportNight = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// reportMediaPrefixes are where a report's images and video may come from:
// the all-sky camera's S3 bucket, or a JPEG embedded in the report. The CSP
// allows the same.
var reportMediaPrefixes = []string{
	"https://deepspaceplace-allsky.s3.ap-southeast-2.amazonaws.com/",
	"data:image/jpeg;base64,",
}

func allowedReportMedia(src string) bool {
	if nightreport.CheckSrc(src) != nil {
		return false
	}
	for _, p := range reportMediaPrefixes {
		if strings.HasPrefix(src, p) {
			return true
		}
	}
	return false
}

// HandleReportUpload is PUT /api/reports/{source}/{night}: a producer
// publishing, or re-publishing, its report for one night.
//
// The bearer key decides what may be written: each source has its own key,
// so skyq's cannot overwrite skycam's report. The report is validated,
// re-encoded (dropping anything the site does not know) and stored; nothing
// in the request becomes a file path. A re-published night replaces its row.
//
// Rejections log at Info: they are driven by whoever sends the request, and
// Warn and above ship to monitor as alerts.
func HandleReportUpload(w http.ResponseWriter, r *http.Request) {
	source, night := r.PathValue("source"), r.PathValue("night")

	key, known := ReportKeys[source]
	if source != nightreport.SourceSkyq && source != nightreport.SourceSkycam {
		known = false
	}
	if !known || key == "" || !bearerMatches(r, key) {
		slog.Info("Report upload refused", "source", source, "night", night, "reason", "bad key")
		w.Header().Set("WWW-Authenticate", `Bearer realm="reports"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := checkReportNight(night, time.Now()); err != nil {
		reportRejected(w, source, night, http.StatusBadRequest, err)
		return
	}

	// Errors are deliberately ignored: a writer that cannot move its
	// deadline (an httptest recorder) simply keeps the server's.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(reportUploadTimeout))
	body := http.MaxBytesReader(w, r.Body, maxReportBody)

	var rep nightreport.Report
	if err := json.NewDecoder(body).Decode(&rep); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			reportRejected(w, source, night, http.StatusRequestEntityTooLarge, err)
			return
		}
		reportRejected(w, source, night, http.StatusBadRequest, fmt.Errorf("decoding: %w", err))
		return
	}
	if err := checkReport(&rep, source, night); err != nil {
		reportRejected(w, source, night, http.StatusBadRequest, err)
		return
	}

	stored, err := json.Marshal(rep)
	if err != nil {
		reportRejected(w, source, night, http.StatusBadRequest, err)
		return
	}
	pills := rep.Pills
	if pills == nil {
		pills = []string{}
	}
	pillsJSON, _ := json.Marshal(pills)
	err = DB.UpsertNightReport(r.Context(), database.UpsertNightReportParams{
		Night: night, Source: source, Body: string(stored),
		Summary: rep.Summary, Pills: string(pillsJSON), ReceivedAt: time.Now().Unix(),
	})
	if err != nil {
		slog.Error("Storing night report", "source", source, "night", night, "error", err)
		http.Error(w, "could not store report", http.StatusInternalServerError)
		return
	}
	slog.Info("Night report stored", "source", source, "night", night, "bytes", len(stored))
	w.WriteHeader(http.StatusNoContent)
}

func bearerMatches(r *http.Request, key string) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(key)) == 1
}

// checkReportNight accepts an evening date from earliestReportNight to
// tomorrow. Tomorrow, not today, so a producer in Melbourne is never refused
// for being ahead of the server's UTC clock.
func checkReportNight(night string, now time.Time) error {
	t, err := time.Parse("2006-01-02", night)
	if err != nil {
		return fmt.Errorf("night %q is not YYYY-MM-DD", night)
	}
	latest := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1)
	if t.Before(earliestReportNight) || t.After(latest) {
		return fmt.Errorf("night %s is outside %s to %s", night,
			earliestReportNight.Format("2006-01-02"), latest.Format("2006-01-02"))
	}
	return nil
}

// checkReport validates a report against the URL it was sent to, and its
// media against the site's allowlist.
func checkReport(rep *nightreport.Report, source, night string) error {
	if err := rep.Validate(); err != nil {
		return err
	}
	if rep.Source != source || rep.Night != night {
		return fmt.Errorf("body is %s/%s, URL is %s/%s", rep.Source, rep.Night, source, night)
	}
	for _, s := range rep.Sections {
		if s.Video != nil && (!allowedReportMedia(s.Video.Src) || (s.Video.Poster != "" && !allowedReportMedia(s.Video.Poster))) {
			return fmt.Errorf("section %q: video is not from an allowed host", s.Heading)
		}
		for _, im := range s.Images {
			if !allowedReportMedia(im.Src) {
				return fmt.Errorf("section %q: image is not from an allowed host", s.Heading)
			}
		}
	}
	return nil
}

func reportRejected(w http.ResponseWriter, source, night string, status int, err error) {
	slog.Info("Report upload rejected", "source", source, "night", night, "status", status, "error", err)
	http.Error(w, err.Error(), status)
}
