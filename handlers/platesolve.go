package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
		log.Printf("Astrometry login failed: %v", err)
		writesolveResult(w, id, "danger", "Login failed")
		return
	}

	// Submit
	imageURL := imageBaseURL + img.Filename
	subID, err := astrometrySubmit(session, imageURL, img.Camera, img.Scope)
	if err != nil {
		log.Printf("Astrometry submit failed for %s: %v", id, err)
		writesolveResult(w, id, "danger", "Submit failed")
		return
	}
	log.Printf("Plate solve submitted for %s: sub=%d", id, subID)

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
		markSolveFailed(ctx, id)
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
		markSolveFailed(ctx, id)
		writesolveResult(w, id, "danger", "Solve failed")
		return
	}

	// Get calibration
	cal, err := astrometryGetCalibration(jobID)
	if err != nil {
		log.Printf("Calibration fetch failed for %s job %d: %v", id, jobID, err)
		markSolveFailed(ctx, id)
		writesolveResult(w, id, "danger", "Calibration error")
		return
	}

	// Update DB
	err = DB.UpdateImagePlateSolve(ctx, database.UpdateImagePlateSolveParams{
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
	})
	if err != nil {
		log.Printf("DB update failed for %s: %v", id, err)
		writesolveResult(w, id, "danger", "DB update failed")
		return
	}

	log.Printf("Plate solve success for %s: RA=%.3f DEC=%.3f", id, cal.RA, cal.DEC)
	writesolveResult(w, id, "success", fmt.Sprintf("RA %.1f Dec %.1f", cal.RA, cal.DEC))
}

func markSolveFailed(ctx context.Context, id string) {
	DB.UpdateImagePlateSolve(ctx, database.UpdateImagePlateSolveParams{
		ID:     id,
		Solved: "f",
	})
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

func astrometrySubmit(session, imageURL, camera, scope string) (int, error) {
	submission := map[string]interface{}{
		"session":              session,
		"url":                  imageURL,
		"allow_commercial_use": "n",
		"allow_modifications":  "n",
		"publicly_visible":     "n",
	}

	scaleHint := getScaleHint(camera, scope)
	if scaleHint > 0 {
		submission["scale_est"] = scaleHint
		submission["scale_err"] = 30.0
		submission["scale_units"] = "arcsecperpix"
		submission["scale_type"] = "ev"
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
