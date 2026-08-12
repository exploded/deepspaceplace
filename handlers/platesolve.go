package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"deepspaceplace/internal/database"
)

const imageBaseURL = "https://deepspaceplace.com/images/"

// astrometryAPIBase is a variable so tests can point the polling loop at a
// stub. Nothing else reassigns it.
var astrometryAPIBase = "https://nova.astrometry.net/api"

// astrometryClient talks to nova with a timeout, which http.DefaultClient does
// not have. A hung connection on the default client blocks its poll iteration
// for as long as the peer keeps the socket open -- which would sail straight
// past the watcher's own deadline and pin the goroutine indefinitely.
var astrometryClient = &http.Client{Timeout: 30 * time.Second}

// solvePollInterval is how often a submission is asked about. Nova is under no
// obligation to be quick and this runs for minutes, so it is deliberately
// unhurried. A variable only so tests need not run in real time.
var solvePollInterval = 5 * time.Second

const (
	// inlineSolveWait is how long the browser waits before the job is handed
	// to a background watcher. The solves on 2026-08-10 came back in 18 to 47
	// seconds, with one outlier at 1m38, so this is already generous -- while
	// staying short enough not to sit near a reverse proxy's read timeout.
	inlineSolveWait = 5 * time.Minute

	// backgroundSolveWait is how much longer the watcher keeps going. Nova
	// reaches a terminal state on any real field long before this; lovejoy1,
	// the worst case on the site, took just under ten minutes end to end.
	// The ceiling exists so a job that never resolves cannot leave a
	// goroutine polling for the life of the process.
	backgroundSolveWait = 30 * time.Minute
)

// solveStatus is the outcome of waiting on a submission.
type solveStatus string

const (
	solveSuccess solveStatus = "success"
	solveFailure solveStatus = "failure" // nova says the field did not solve
	solvePending solveStatus = "pending" // still running when we stopped waiting
	solveError   solveStatus = "error"   // our side broke; the row is untouched
)

// solveOutcome is what awaitSolve settled on. Cal is set only on success.
type solveOutcome struct {
	Status solveStatus
	Cal    *calibration
}

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
		writesolveResult(w, id, "danger", "No image ID", nil)
		return
	}

	ctx := r.Context()
	img, err := DB.GetImage(ctx, id)
	if err != nil {
		writesolveResult(w, id, "danger", "Image not found", nil)
		return
	}

	apiKey := os.Getenv("ASTROMETRY_API_KEY")
	if apiKey == "" {
		writesolveResult(w, id, "danger", "API key not configured", nil)
		return
	}

	// Login
	session, err := astrometryLogin(apiKey)
	if err != nil {
		slog.Error("Astrometry login failed", "error", err)
		writesolveResult(w, id, "danger", "Login failed", nil)
		return
	}

	// Submit
	imageURL := imageBaseURL + img.Filename
	bounds := scaleBounds(img)
	subID, err := astrometrySubmit(session, imageURL, bounds)
	if err != nil {
		slog.Error("Astrometry submit failed", "id", id, "error", err)
		writesolveResult(w, id, "danger", "Submit failed", nil)
		return
	}
	slog.Info("Plate solve submitted", "id", id, "sub", subID,
		"scale_lower", bounds.Lower, "scale_upper", bounds.Upper, "scale_source", bounds.Source)

	// Record the submission before polling anything. Until the id is on the
	// row it exists only in this stack frame, and everything that ends the
	// request -- a timeout, a panic, a deploy -- throws away the only handle
	// on a job nova is still working on.
	markSolvePending(id, subID)

	ctx, cancel := context.WithTimeout(context.Background(), inlineSolveWait)
	defer cancel()
	out := awaitSolve(ctx, id, subID)

	if out.Status == solvePending {
		// Not a failure, and pointedly not recorded as one: the job is still
		// running and the row stays 'p' with its submission id, so the watcher
		// can finish the write whenever nova gets there.
		slog.Info("Plate solve still running, handed to background watcher",
			"id", id, "sub", subID, "waited", inlineSolveWait)
		watchSolve(id, subID, backgroundSolveWait)
		writeSolveCell(w, solveCell{ID: id, Class: "info", Msg: "Solving…", Polling: true})
		return
	}

	writeSolveCell(w, solveCellFor(id, out))
}

// awaitSolve polls a submission through to a terminal state and records it.
//
// This is the only place a solve result is written, so the inline wait, the
// background watcher and the boot-time resume cannot come to different
// conclusions about what an outcome means. A solvePending return means the
// context expired with the job still going; the caller decides whether to hand
// off or give up, and either way the row is left alone.
func awaitSolve(ctx context.Context, id string, subID int) solveOutcome {
	var jobID int
	for {
		// Wait first: a submission accepted a moment ago has no job yet.
		select {
		case <-ctx.Done():
			return solveOutcome{Status: solvePending}
		case <-time.After(solvePollInterval):
		}

		if jobID == 0 {
			jobID = astrometryCheckSubmission(subID)
			continue
		}

		switch astrometryCheckJob(jobID) {
		case "success":
			cal, err := astrometryGetCalibration(jobID)
			if err != nil {
				slog.Error("Calibration fetch failed", "id", id, "job", jobID, "error", err)
				markSolveFailed(id)
				return solveOutcome{Status: solveFailure}
			}
			if err := recordSolve(id, cal); err != nil {
				slog.Error("DB update failed", "id", id, "job", jobID, "error", err)
				return solveOutcome{Status: solveError}
			}
			slog.Info("Plate solve success", "id", id, "sub", subID, "job", jobID,
				"ra", cal.RA, "dec", cal.DEC)
			return solveOutcome{Status: solveSuccess, Cal: cal}

		case "failure":
			slog.Info("Plate solve failed", "id", id, "sub", subID, "job", jobID)
			markSolveFailed(id)
			return solveOutcome{Status: solveFailure}
		}
	}
}

// watching guards against two goroutines polling the same row -- a double
// click on Solve, or a resume racing a fresh submission. The loser is dropped
// rather than queued: both would be watching the same job for the same answer.
var watching sync.Map // image id -> struct{}

// watchSolve keeps polling a submission after the request that started it has
// gone, and writes the result whenever it lands.
func watchSolve(id string, subID int, wait time.Duration) {
	if _, busy := watching.LoadOrStore(id, struct{}{}); busy {
		slog.Info("Solve watcher already running, not starting another", "id", id, "sub", subID)
		return
	}
	go func() {
		defer watching.Delete(id)

		ctx, cancel := context.WithTimeout(context.Background(), wait)
		defer cancel()

		if awaitSolve(ctx, id, subID).Status != solvePending {
			return
		}

		// Out of patience. Recording this as a failure is a compromise: the
		// job may yet succeed, and saying otherwise is the very lie this
		// change set out to remove. But 'p' is not a state anything clears on
		// its own, and a row stuck there polls the admin page forever with
		// nobody listening. So the state machine terminates here, loudly, with
		// the submission id in the log -- the result stays recoverable from
		// nova by hand, which is exactly what was impossible before.
		slog.Warn("Gave up waiting on plate solve; job may still complete on nova",
			"id", id, "sub", subID, "waited", wait,
			"recover", fmt.Sprintf("%s/submissions/%d", astrometryAPIBase, subID))
		markSolveFailed(id)
	}()
}

// ResumePendingSolves picks up solves that a restart interrupted.
//
// Without this the durable submission id would buy nothing across a deploy:
// the row would sit at 'p' with a perfectly good job running on nova and no
// process left watching for its result.
func ResumePendingSolves() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pending, err := DB.ListPendingSolves(ctx)
	if err != nil {
		slog.Error("Could not list pending solves to resume", "error", err)
		return
	}
	for _, p := range pending {
		slog.Info("Resuming plate solve interrupted by restart", "id", p.ID, "sub", p.SolveSubid.Int64)
		watchSolve(p.ID, int(p.SolveSubid.Int64), backgroundSolveWait)
	}
}

// recordSolve writes a successful calibration.
//
// It uses a background context throughout: by the time a result arrives the
// request that asked for it has usually gone, and on the watcher path there
// was never a request to begin with.
func recordSolve(id string, cal *calibration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return DB.UpdateImagePlateSolve(ctx, database.UpdateImagePlateSolveParams{
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
}

// markSolvePending records an in-flight submission against the row.
func markSolvePending(id string, subID int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := DB.MarkSolvePending(ctx, database.MarkSolvePendingParams{
		ID:         id,
		SolveSubid: sql.NullInt64{Int64: int64(subID), Valid: true},
	})
	if err != nil {
		// Not fatal -- the inline wait and the watcher both hold subID in
		// memory and will still finish. Only a restart loses the job now.
		slog.Error("Could not record pending solve", "id", id, "sub", subID, "error", err)
	}
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

// solveCell is one rendering of the admin list's solve cell.
type solveCell struct {
	ID      string
	Class   string // Bootstrap contextual colour
	Msg     string
	Button  string // offered unless Polling
	Polling bool   // job still running: poll instead of offering a button
	Overlay *OverlayStatus
}

// solveCellTmpl renders that cell.
//
// A button is offered on every terminal state, including success: re-solving a
// green row used to mean editing the record to set solved back to "n", and
// there are real reasons to want one -- a row solved before parity was
// captured, or one whose stored field no longer matches a since-downscaled
// file.
//
// While a solve is running the button is replaced by a spinner that re-fetches
// the cell, because the useful action there is waiting, and a live Solve button
// would submit a second job for a field nova is already working on.
//
// The refresh is "load delay:15s" rather than an "every" trigger so it is
// self-limiting: each fetch replaces the element, and the replacement only
// carries another trigger while the answer is still pending. A terminal
// fragment simply has no trigger, and the polling stops.
//
// When the outcome changed the overlay status it is swapped out of band,
// because a solve is precisely the thing that changes it and leaving a stale
// "rotation never measured" beside a fresh green badge would be actively
// misleading. The badge is a span rather than the cell itself so the fragment
// survives the browser's table parsing.
var solveCellTmpl = template.Must(template.New("solvecell").Parse(
	`<span class="badge bg-{{.Class}}">{{.Msg}}</span>` +
		`{{if .Polling}} <span class="spinner-border spinner-border-sm text-info" role="status"` +
		` hx-get="/admin/solvestatus?id={{.ID}}" hx-trigger="load delay:15s"` +
		` hx-target="#solve-{{.ID}}" hx-swap="innerHTML"></span>` +
		`{{else if .ID}} <button class="btn btn-sm btn-outline-info" hx-post="/admin/platesolve"` +
		` hx-vals='{"id": "{{.ID}}"}' hx-target="#solve-{{.ID}}" hx-swap="innerHTML"` +
		` hx-disabled-elt="this">{{.Button}}</button>{{end}}` +
		`{{with .Overlay}}<span id="overlay-{{$.ID}}" hx-swap-oob="true"` +
		` class="badge {{.BadgeClass}}">{{.BadgeText}}</span>{{end}}`))

// solveCellFor turns a settled outcome into a cell, reading the stored row back
// rather than trusting the calibration in hand, so the overlay badge reports on
// what is actually in the database -- including the geometry check against the
// file on disk, which the calibration response knows nothing about.
func solveCellFor(id string, out solveOutcome) solveCell {
	cell := solveCell{ID: id, Button: "Retry"}

	switch out.Status {
	case solveSuccess:
		cell.Class, cell.Button = "success", "Re-solve"
		cell.Msg = fmt.Sprintf("RA %.1f Dec %.1f", out.Cal.RA, out.Cal.DEC)
	case solveFailure:
		cell.Class, cell.Msg = "danger", "Solve failed"
	case solvePending:
		// The handler hands these to the watcher before getting here, so this
		// is unreachable today. It is spelled out anyway because the wrong
		// branch would report a running job as broken -- the original bug.
		cell.Class, cell.Msg, cell.Polling = "info", "Solving…", true
	default:
		cell.Class, cell.Msg = "danger", "DB update failed"
	}

	// Only a success moves the astrometry columns, so only a success can move
	// the overlay badge; leaving it alone elsewhere is deliberate.
	if out.Status == solveSuccess {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if updated, err := DB.GetImage(ctx, id); err != nil {
			slog.Error("Could not re-read image to report overlay status", "id", id, "error", err)
		} else {
			st := CheckOverlay(updated)
			cell.Overlay = &st
		}
	}
	return cell
}

func writeSolveCell(w http.ResponseWriter, cell solveCell) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := solveCellTmpl.Execute(w, cell); err != nil {
		slog.Error("Failed to render solve cell", "id", cell.ID, "error", err)
	}
}

// writesolveResult reports an outcome the solve never got far enough to have --
// a missing id, a bad key, a submission nova refused.
func writesolveResult(w http.ResponseWriter, id, badgeClass, msg string, overlay *OverlayStatus) {
	writeSolveCell(w, solveCell{ID: id, Class: badgeClass, Msg: msg, Button: "Retry", Overlay: overlay})
}

// HandleAdminSolveStatus redraws the solve cell for a row.
//
// It only reads the database. The watcher goroutine is the single poller of any
// given submission -- respawned at boot by ResumePendingSolves, so a row at 'p'
// always has exactly one -- which keeps this endpoint free to be hit every 15
// seconds by every open admin tab without multiplying traffic to nova.
func HandleAdminSolveStatus(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "No image ID", http.StatusBadRequest)
		return
	}

	img, err := DB.GetImage(r.Context(), id)
	if err != nil {
		writesolveResult(w, id, "danger", "Image not found", nil)
		return
	}

	if img.Solved == "p" {
		writeSolveCell(w, solveCell{ID: id, Class: "info", Msg: "Solving…", Polling: true})
		return
	}

	// Settled while we were polling. Report the row as the admin list would
	// have drawn it, including the overlay badge, which a completed solve is
	// the most likely thing to have changed.
	overlay := CheckOverlay(img)
	cell := solveCell{ID: id, Button: "Retry", Overlay: &overlay}
	switch img.Solved {
	case "y":
		cell.Class, cell.Button = "success", "Re-solve"
		cell.Msg = fmt.Sprintf("RA %.1f Dec %.1f", img.Ra.Float64, img.Dec.Float64)
	case "f":
		cell.Class, cell.Msg = "danger", "Solve failed"
	default:
		cell.Class, cell.Msg, cell.Button = "secondary", "Not solved", "Solve"
	}
	writeSolveCell(w, cell)
}

// --- Astrometry.net API ---

func astrometryLogin(apiKey string) (string, error) {
	payload := fmt.Sprintf(`{"apikey": "%s"}`, apiKey)
	resp, err := astrometryClient.PostForm(astrometryAPIBase+"/login", url.Values{
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
	resp, err := astrometryClient.PostForm(astrometryAPIBase+"/url_upload", url.Values{
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
	resp, err := astrometryClient.Get(fmt.Sprintf("%s/submissions/%d", astrometryAPIBase, subID))
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
	resp, err := astrometryClient.Get(fmt.Sprintf("%s/jobs/%d", astrometryAPIBase, jobID))
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
	resp, err := astrometryClient.Get(fmt.Sprintf("%s/jobs/%d/calibration/", astrometryAPIBase, jobID))
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
