package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"deepspaceplace/internal/database"
)

const (
	astrometryAPIBase = "https://nova.astrometry.net/api"
	imageBaseURL      = "https://deepspaceplace.com/images/"
)

type calibration struct {
	RA          float64 `json:"ra"`
	DEC         float64 `json:"dec"`
	PixScale    float64 `json:"pixscale"`
	Radius      float64 `json:"radius"`
	WidthAS     float64 `json:"width_arcsec"`
	HeightAS    float64 `json:"height_arcsec"`
	FieldW      float64 `json:"fieldw"`
	FieldH      float64 `json:"fieldh"`
	Orientation float64 `json:"orientation"`
	// Parity says whether the image is mirrored. Without it the annotation
	// overlay has to guess, and a wrong guess flips every label across the
	// frame. Astrometry.net has always returned it; this code just never
	// asked for it, which is why rows solved before now have it as NULL.
	Parity float64 `json:"parity"`
}

func HandleAdminPlateSolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extend write deadline — solving takes minutes
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))

	r.ParseForm()
	if !validateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	id := r.FormValue("id")
	if id == "" {
		writesolveResult(w, id, "danger", "No image ID")
		return
	}

	ctx := r.Context()
	img, err := DB.GetImage(ctx, id)
	if err != nil {
		writesolveResult(w, id, "danger", "Image not found")
		return
	}

	apiKey := os.Getenv("ASTROMETRY_API_KEY")
	if apiKey == "" {
		writesolveResult(w, id, "danger", "API key not configured")
		return
	}

	// Login
	session, err := astrometryLogin(apiKey)
	if err != nil {
		slog.Error("Astrometry login failed", "error", err)
		writesolveResult(w, id, "danger", "Login failed")
		return
	}

	// Submit
	imageURL := imageBaseURL + img.Filename
	bounds := scaleBounds(img)
	subID, err := astrometrySubmit(session, imageURL, bounds)
	if err != nil {
		slog.Error("Astrometry submit failed", "id", id, "error", err)
		writesolveResult(w, id, "danger", "Submit failed")
		return
	}
	slog.Info("Plate solve submitted", "id", id, "sub", subID,
		"scale_lower", bounds.Lower, "scale_upper", bounds.Upper, "scale_source", bounds.Source)

	// Poll for job ID
	var jobID int
	for i := 0; i < 30; i++ {
		time.Sleep(5 * time.Second)
		jobID = astrometryCheckSubmission(subID)
		if jobID > 0 {
			break
		}
	}
	if jobID == 0 {
		markSolveFailed(id)
		writesolveResult(w, id, "warning", "Timeout waiting for job")
		return
	}

	// Poll for result
	var status string
	for i := 0; i < 60; i++ {
		time.Sleep(5 * time.Second)
		status = astrometryCheckJob(jobID)
		if status == "success" || status == "failure" {
			break
		}
	}

	if status != "success" {
		markSolveFailed(id)
		writesolveResult(w, id, "danger", "Solve failed")
		return
	}

	// Get calibration
	cal, err := astrometryGetCalibration(jobID)
	if err != nil {
		slog.Error("Calibration fetch failed", "id", id, "job", jobID, "error", err)
		markSolveFailed(id)
		writesolveResult(w, id, "danger", "Calibration error")
		return
	}

	// Update DB — use background context so the write succeeds even if the
	// HTTP client disconnected during the long polling phase.
	dbCtx, dbCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dbCancel()
	err = DB.UpdateImagePlateSolve(dbCtx, database.UpdateImagePlateSolveParams{
		ID:           id,
		Solved:       "y",
		Ra:           sql.NullFloat64{Float64: cal.RA, Valid: true},
		Dec:          sql.NullFloat64{Float64: cal.DEC, Valid: true},
		Pixscale:     sql.NullFloat64{Float64: cal.PixScale, Valid: true},
		Radius:       sql.NullFloat64{Float64: cal.Radius, Valid: true},
		WidthArcsec:  sql.NullFloat64{Float64: cal.WidthAS, Valid: true},
		HeightArcsec: sql.NullFloat64{Float64: cal.HeightAS, Valid: true},
		Fieldw:       sql.NullFloat64{Float64: cal.FieldW, Valid: true},
		Fieldh:       sql.NullFloat64{Float64: cal.FieldH, Valid: true},
		Orientation:  sql.NullFloat64{Float64: cal.Orientation, Valid: true},
		Parity:       sql.NullFloat64{Float64: cal.Parity, Valid: true},
	})
	if err != nil {
		slog.Error("DB update failed", "id", id, "error", err)
		writesolveResult(w, id, "danger", "DB update failed")
		return
	}

	slog.Info("Plate solve success", "id", id, "ra", cal.RA, "dec", cal.DEC)
	writesolveResult(w, id, "success", fmt.Sprintf("RA %.1f Dec %.1f", cal.RA, cal.DEC))
}

// markSolveFailed flags a failed solve, leaving any existing solution intact.
//
// It deliberately does not go through UpdateImagePlateSolve: that query assigns
// every astrometry column, so calling it with only the id and status set --
// which is what this used to do -- nulled out a good RA and Dec on any failed
// RE-solve, quietly removing the image from the skymap and its annotation
// overlay.
func markSolveFailed(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := DB.MarkSolveFailed(ctx, id); err != nil {
		slog.Error("Failed to record solve failure", "id", id, "error", err)
	}
}

func writesolveResult(w http.ResponseWriter, id, badgeClass, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var retry string
	if badgeClass != "success" {
		retry = fmt.Sprintf(` <button class="btn btn-sm btn-outline-info" hx-post="/admin/platesolve" hx-vals='{"id":"%s"}' hx-target="#solve-%s" hx-swap="innerHTML" hx-disabled-elt="this">Retry</button>`, id, id)
	}
	fmt.Fprintf(w, `<span class="badge bg-%s">%s</span>%s`, badgeClass, msg, retry)
}

// --- Astrometry.net API ---

func astrometryLogin(apiKey string) (string, error) {
	payload := fmt.Sprintf(`{"apikey": "%s"}`, apiKey)
	resp, err := http.PostForm(astrometryAPIBase+"/login", url.Values{
		"request-json": {payload},
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if status, ok := result["status"].(string); ok && status == "success" {
		return result["session"].(string), nil
	}
	return "", fmt.Errorf("login failed: %v", result)
}

// scaleRange is the band of pixel scales to tell Astrometry.net to search, in
// arcseconds per pixel. A zero Lower means "no constraint, solve blind".
type scaleRange struct {
	Lower, Upper float64
	Source       string // how it was derived, for the log
}

// scaleBounds works out what pixel scale the file we are about to submit
// actually has.
//
// This is not the same as the scale of the camera at native resolution, and
// conflating the two silently broke solving for any downscaled image. The admin
// resizer rewrites files in place at up to 3840px, so a picture taken at 2.0
// arcsec/pixel and since halved is really at 4.0 -- and a hint of 2.0 with the
// old +/-30% window told the solver to search 1.4 to 2.6, a range the right
// answer could not be in. m45 (true scale 4.2, hint 2.0) and NGC 1871 (2.5
// against 1.6) both failed for exactly this reason.
//
// Preference order:
//
//  1. A previous solve's angular width divided by the file's measured width.
//     This is by far the sharpest estimate, and it stays correct through both
//     downscaling and cropping, because the stored width and the pixel width
//     shrink together.
//  2. The camera-and-scope lookup, treated as a LOWER bound only. Files are
//     only ever downscaled, never enlarged, so the true scale can only be
//     coarser than the native figure.
//  3. Nothing, and let the solver work blind.
func scaleBounds(img database.Image) scaleRange {
	if w, _, ok := imageDims(img.Filename); ok && img.WidthArcsec.Valid && img.WidthArcsec.Float64 > 0 {
		est := img.WidthArcsec.Float64 / float64(w)
		// Generous either side: the stored width may itself be from a solve of
		// a differently sized file.
		return scaleRange{Lower: est * 0.5, Upper: est * 2, Source: "measured from the file and its recorded field width"}
	}

	if hint := getScaleHint(img.Camera, img.Scope); hint > 0 {
		// Upper bound allows for roughly a six-fold downscale, which is more
		// than the resizer can produce from any camera here.
		return scaleRange{Lower: hint * 0.7, Upper: hint * 6, Source: "camera and scope, widened for downscaling"}
	}

	return scaleRange{Source: "none, solving blind"}
}

func astrometrySubmit(session, imageURL string, scale scaleRange) (int, error) {
	submission := map[string]interface{}{
		"session":              session,
		"url":                  imageURL,
		"allow_commercial_use": "n",
		"allow_modifications":  "n",
		"publicly_visible":     "n",
	}

	if scale.Lower > 0 && scale.Upper > scale.Lower {
		submission["scale_type"] = "ul"
		submission["scale_units"] = "arcsecperpix"
		submission["scale_lower"] = scale.Lower
		submission["scale_upper"] = scale.Upper
	}

	jsonBytes, _ := json.Marshal(submission)
	resp, err := http.PostForm(astrometryAPIBase+"/url_upload", url.Values{
		"request-json": {string(jsonBytes)},
	})
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if status, ok := result["status"].(string); ok && status == "success" {
		return int(result["subid"].(float64)), nil
	}
	return 0, fmt.Errorf("submit failed: %v", result)
}

func astrometryCheckSubmission(subID int) int {
	resp, err := http.Get(fmt.Sprintf("%s/submissions/%d", astrometryAPIBase, subID))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if jobs, ok := result["jobs"].([]interface{}); ok {
		for _, j := range jobs {
			if j != nil {
				return int(j.(float64))
			}
		}
	}
	return 0
}

func astrometryCheckJob(jobID int) string {
	resp, err := http.Get(fmt.Sprintf("%s/jobs/%d", astrometryAPIBase, jobID))
	if err != nil {
		return "unknown"
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)

	if status, ok := result["status"].(string); ok {
		return status
	}
	return "unknown"
}

func astrometryGetCalibration(jobID int) (*calibration, error) {
	resp, err := http.Get(fmt.Sprintf("%s/jobs/%d/calibration/", astrometryAPIBase, jobID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var cal calibration
	if err := json.NewDecoder(resp.Body).Decode(&cal); err != nil {
		return nil, err
	}

	if cal.WidthAS > 0 {
		cal.FieldW = cal.WidthAS / 60.0
	}
	if cal.HeightAS > 0 {
		cal.FieldH = cal.HeightAS / 60.0
	}
	if cal.FieldW > 0 && cal.FieldH > 0 {
		wDeg := cal.FieldW / 60.0
		hDeg := cal.FieldH / 60.0
		cal.Radius = math.Sqrt(wDeg*wDeg+hDeg*hDeg) / 2.0
	}

	return &cal, nil
}

func getScaleHint(camera, scope string) float64 {
	switch {
	case camera == "ASI2600MM DUO" && strings.Contains(scope, "AT12"):
		return 0.671
	case camera == "STL-11000M" && strings.Contains(scope, "AT12"):
		return 1.6
	case camera == "STL-11000M" && strings.Contains(scope, "GSO 8 RC"):
		return 1.14
	case camera == "STL-11000M" && strings.Contains(scope, "AT8"):
		return 2.0
	case camera == "Nikon D50" && strings.Contains(scope, "ED127"):
		return 1.69
	case camera == "QHY9" && strings.Contains(scope, "GSO 8 RC"):
		return 0.699
	case camera == "Canon 500D" && strings.Contains(scope, "GSO 8 RC"):
		return 0.609
	case camera == "ASI2600MM DUO" && strings.Contains(scope, "TOA130"):
		return 0.773
	default:
		return 0
	}
}
