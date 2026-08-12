package handlers

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deepspaceplace/internal/database"

	_ "modernc.org/sqlite"
)

// withTestDB points the package DB at a fresh in-memory database carrying one
// image row, and returns its id.
func withTestDB(t *testing.T) string {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	schema, err := os.ReadFile(filepath.Join("..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := conn.Exec(string(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO images (id, filename) VALUES ('lovejoy1', 'lovejoy1.jpg')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	old := DB
	DB = database.New(conn)
	t.Cleanup(func() { DB = old })
	return "lovejoy1"
}

func readRow(t *testing.T, id string) database.Image {
	t.Helper()
	img, err := DB.GetImage(context.Background(), id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return img
}

// stubNova stands in for nova.astrometry.net. jobStatus is consulted on every
// /jobs/N poll, so a test can keep a job running for as long as it likes.
func stubNova(t *testing.T, jobStatus func() string) *int32 {
	t.Helper()

	var jobPolls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/submissions/"):
			fmt.Fprint(w, `{"jobs": [16651021]}`)
		case strings.HasSuffix(r.URL.Path, "/calibration/"):
			fmt.Fprint(w, `{"ra": 247.126, "dec": -53.348, "pixscale": 104.4155,
				"width_arcsec": 165394.2, "height_arcsec": 248091.3,
				"orientation": 133.358, "parity": 1.0}`)
		case strings.Contains(r.URL.Path, "/jobs/"):
			atomic.AddInt32(&jobPolls, 1)
			fmt.Fprintf(w, `{"status": %q}`, jobStatus())
		default:
			t.Errorf("unexpected call to %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	oldBase, oldInterval := astrometryAPIBase, solvePollInterval
	astrometryAPIBase = srv.URL
	solvePollInterval = 5 * time.Millisecond
	t.Cleanup(func() { astrometryAPIBase, solvePollInterval = oldBase, oldInterval })

	return &jobPolls
}

// TestSlowSolveIsNotRecordedAsFailed is the regression guard for lovejoy1.
//
// Nova spent 574 seconds on that field. The old code polled for 300, decided
// that silence meant failure, and wrote solved='f' over a job that went on to
// succeed -- discarding the submission id, which was the only way back to the
// result. Running out of patience is not an outcome, and must not be recorded
// as one.
func TestSlowSolveIsNotRecordedAsFailed(t *testing.T) {
	id := withTestDB(t)
	stubNova(t, func() string { return "solving" })

	markSolvePending(id, 15814827)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if got := awaitSolve(ctx, id, 15814827).Status; got != solvePending {
		t.Fatalf("status = %q, want %q", got, solvePending)
	}

	row := readRow(t, id)
	if row.Solved != "p" {
		t.Errorf("solved = %q, want %q -- a job still running was recorded as settled", row.Solved, "p")
	}
	if !row.SolveSubid.Valid || row.SolveSubid.Int64 != 15814827 {
		t.Errorf("submission id = %v, want 15814827 kept on the row; "+
			"without it the running job is unreachable", row.SolveSubid)
	}
}

// The whole point of persisting the submission id: a solve that outlives the
// process it was started from can still be finished by the next one.
func TestPendingSolveResumesAfterRestart(t *testing.T) {
	id := withTestDB(t)

	var status atomic.Value
	status.Store("solving")
	stubNova(t, func() string { return status.Load().(string) })

	markSolvePending(id, 15814827)

	// Stand in for the restart: nothing is watching, the row is all we have.
	pending, err := DB.ListPendingSolves(context.Background())
	if err != nil {
		t.Fatalf("list pending: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != id {
		t.Fatalf("pending = %+v, want the one in-flight row", pending)
	}

	ResumePendingSolves()
	status.Store("success")

	waitFor(t, func() bool { return readRow(t, id).Solved == "y" },
		"resumed watcher never recorded the result")

	row := readRow(t, id)
	if !row.Ra.Valid || row.Ra.Float64 < 247 || row.Ra.Float64 > 248 {
		t.Errorf("ra = %v, want the calibration written", row.Ra)
	}
	if row.SolveSubid.Valid {
		t.Error("submission id survived a completed solve; it should be cleared")
	}
}

// A genuine failure still settles the row -- and still leaves the astrometry
// columns alone, so a failed re-solve cannot blank a good solution.
func TestRealFailureIsRecordedAndClearsSubmission(t *testing.T) {
	id := withTestDB(t)
	stubNova(t, func() string { return "failure" })

	markSolvePending(id, 15814827)
	if got := awaitSolve(context.Background(), id, 15814827).Status; got != solveFailure {
		t.Fatalf("status = %q, want %q", got, solveFailure)
	}

	row := readRow(t, id)
	if row.Solved != "f" {
		t.Errorf("solved = %q, want %q", row.Solved, "f")
	}
	if row.SolveSubid.Valid {
		t.Error("submission id survived a settled solve; it should be cleared")
	}
}

// Two watchers on one row would poll nova twice for one answer, and after a
// resume raced a fresh submission they could write different ones.
func TestWatchSolveRefusesADuplicateWatcher(t *testing.T) {
	id := withTestDB(t)

	var status atomic.Value
	status.Store("solving")
	polls := stubNova(t, func() string { return status.Load().(string) })

	markSolvePending(id, 15814827)
	watchSolve(id, 15814827, time.Minute)
	waitFor(t, func() bool { return atomic.LoadInt32(polls) > 0 }, "first watcher never polled")

	watchSolve(id, 15814827, time.Minute) // must be dropped

	before := atomic.LoadInt32(polls)
	time.Sleep(60 * time.Millisecond)
	elapsed := atomic.LoadInt32(polls) - before

	// One watcher at a 5ms interval manages roughly a dozen polls in 60ms;
	// two would roughly double that. The bound is loose enough to survive a
	// slow CI box and tight enough to catch a second poller.
	if elapsed > 24 {
		t.Errorf("%d polls in 60ms suggests more than one watcher is running", elapsed)
	}

	status.Store("success")
	waitFor(t, func() bool { return readRow(t, id).Solved == "y" }, "watcher never finished")
}

// A pending cell must offer no Solve button -- clicking one would submit a
// second job for a field nova is already working on -- and must carry the
// trigger that fetches it again.
func TestPendingCellPollsInsteadOfOfferingSolve(t *testing.T) {
	id := withTestDB(t)
	markSolvePending(id, 15814827)

	rec := httptest.NewRecorder()
	HandleAdminSolveStatus(rec, httptest.NewRequest(http.MethodGet, "/admin/solvestatus?id="+id, nil))
	body := rec.Body.String()

	for _, want := range []string{
		`hx-get="/admin/solvestatus?id=lovejoy1"`,
		`hx-trigger="load delay:15s"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("pending cell missing %s\ngot: %s", want, body)
		}
	}
	if strings.Contains(body, "<button") {
		t.Errorf("pending cell offers a button, which would double-submit\ngot: %s", body)
	}
}

// Once the row settles the fragment must stop carrying a trigger, or the admin
// page polls a finished solve forever.
func TestSettledCellStopsPolling(t *testing.T) {
	id := withTestDB(t)
	withFixtures(t, 3840, 2560)
	stubNova(t, func() string { return "success" })

	markSolvePending(id, 15814827)
	if got := awaitSolve(context.Background(), id, 15814827).Status; got != solveSuccess {
		t.Fatalf("status = %q, want %q", got, solveSuccess)
	}

	rec := httptest.NewRecorder()
	HandleAdminSolveStatus(rec, httptest.NewRequest(http.MethodGet, "/admin/solvestatus?id="+id, nil))
	body := rec.Body.String()

	if strings.Contains(body, "hx-trigger") {
		t.Errorf("settled cell still polls\ngot: %s", body)
	}
	if !strings.Contains(body, ">Re-solve</button>") {
		t.Errorf("settled cell should offer a re-solve\ngot: %s", body)
	}
}

// The admin list is the other way into a pending row -- reloading the page
// mid-solve, or opening it after a restart -- and it has its own copy of the
// cell markup, so it needs the same guarantee the HTMX fragment has.
func TestAdminListShowsPendingRowAsPolling(t *testing.T) {
	tmpl, err := template.New("list.html").Funcs(TemplateFuncs).
		ParseFiles(filepath.Join("..", "templates", "admin", "list.html"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var buf bytes.Buffer
	err = tmpl.ExecuteTemplate(&buf, "content", struct {
		Images    []adminImageRow
		CSRFToken string
	}{
		Images: []adminImageRow{{
			Image:   database.Image{ID: "lovejoy1", Filename: "lovejoy1.jpg", Solved: "p"},
			Overlay: OverlayStatus{Reason: "never solved", Resolvable: true},
		}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()

	if !strings.Contains(body, `hx-get="/admin/solvestatus?id=lovejoy1"`) {
		t.Errorf("pending row does not poll for its result\ngot: %s", body)
	}
	if strings.Contains(body, "/admin/platesolve") {
		t.Errorf("pending row offers a solve, which would submit a second job\ngot: %s", body)
	}
}

// TestMigratedDatabaseServesTheGeneratedQueries covers the upgrade path this
// change actually takes in production.
//
// The live database already has the images table, so schema.sql -- which only
// ever runs as CREATE TABLE IF NOT EXISTS -- does nothing to it, and the new
// column arrives solely through addMissingColumns. Meanwhile sqlc has already
// expanded "SELECT *" into a list that names solve_subid. Get that wrong and
// every query on the site fails with "no such column" the moment the new
// binary starts: not a subtle bug, but a total one, and invisible until deploy.
//
// The pre-migration table is rebuilt from the real schema rather than from a
// hand-written copy, so it cannot drift out of step.
func TestMigratedDatabaseServesTheGeneratedQueries(t *testing.T) {
	conn, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer conn.Close()

	schema, err := os.ReadFile(filepath.Join("..", "db", "schema.sql"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := conn.Exec(string(schema)); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// Wind back to how an existing production database looks. The column list
	// is read out of the table just created rather than written by hand, so
	// this stays honest as the schema grows.
	old, found := columnsExcept(t, conn, "solve_subid")
	if !found {
		t.Fatal("schema.sql no longer declares solve_subid")
	}
	if _, err := conn.Exec(`DROP TABLE images`); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE images (` + strings.Join(old, ", ") + `)`); err != nil {
		t.Fatalf("recreate pre-migration table: %v", err)
	}

	// Now apply the migration cmd/server runs at boot.
	if _, err := conn.Exec(`ALTER TABLE images ADD COLUMN solve_subid INTEGER`); err != nil {
		t.Fatalf("add column: %v", err)
	}

	_, err = conn.Exec(`INSERT INTO images (id, filename, solved, orientation, parity, solve_subid)
		VALUES ('lovejoy1', 'lovejoy1.jpg', 'p', 133.358, 1.0, 15814827)`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	prev := DB
	DB = database.New(conn)
	t.Cleanup(func() { DB = prev })

	img := readRow(t, "lovejoy1")
	for _, c := range []struct {
		field string
		got   any
		want  any
	}{
		{"filename", img.Filename, "lovejoy1.jpg"},
		{"solved", img.Solved, "p"},
		{"orientation", img.Orientation.Float64, 133.358},
		{"parity", img.Parity.Float64, 1.0},
		{"solve_subid", img.SolveSubid.Int64, int64(15814827)},
	} {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v -- a migrated database does not read back correctly",
				c.field, c.got, c.want)
		}
	}
}

// columnsExcept reads a table's columns back as DDL fragments, omitting the
// named one, and reports whether it was there to omit.
func columnsExcept(t *testing.T, conn *sql.DB, omit string) (defs []string, found bool) {
	t.Helper()

	rows, err := conn.Query(`PRAGMA table_info(images)`)
	if err != nil {
		t.Fatalf("table_info: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid           int
			name, colType string
			notNull, pk   int
			dflt          sql.NullString
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if name == omit {
			found = true
			continue
		}
		def := name + " " + colType
		if pk == 1 {
			def += " PRIMARY KEY"
		}
		if notNull == 1 {
			def += " NOT NULL"
		}
		if dflt.Valid {
			def += " DEFAULT " + dflt.String
		}
		defs = append(defs, def)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("table_info rows: %v", err)
	}
	return defs, found
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal(msg)
}
