package handlers

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"deepspaceplace/internal/database"

	_ "modernc.org/sqlite"
)

// legacyDB builds a throwaway database holding the rows that matter for legacy
// id recovery. The ids and filenames are copied verbatim from db/seed.sql --
// the pairing of a re-keyed id with its old filename is the whole mechanism, so
// inventing values here would test nothing.
func legacyDB(t *testing.T) *database.Queries {
	t.Helper()

	db, err := sql.Open("sqlite", "file:legacy?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	schema, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		t.Fatalf("apply schema: %v", err)
	}

	rows := []struct{ id, filename string }{
		{"m047", "ngc2422.jpg"},     // re-keyed NGC 2422 -> M 47
		{"m042", "ngc1976.jpg"},     // re-keyed NGC 1976 -> M 42
		{"m008", "ngc6523.jpg"},     // re-keyed NGC 6523 -> M 8
		{"ngc0253", "ngc253.jpg"},   // zero-padded
		{"ngc0253b", "ngc253b.jpg"}, // zero-padded, with a suffix
		{"rcw007", "rcw7.jpg"},      // zero-padded to three
		{"ic0434", "ic434.jpg"},     // zero-padded to four
		{"ngc2477", "ngc2477.jpg"},  // never changed
		{"sh2-280", "sh2-280.jpg"},  // not prefix-digits shaped
		{"moon", "moon.JPG"},        // no digits at all, uppercase extension
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO images (id, filename) VALUES (?, ?)`, r.id, r.filename); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return database.New(db)
}

func withLegacyDB(t *testing.T) {
	t.Helper()
	old := DB
	DB = legacyDB(t)
	t.Cleanup(func() { DB = old })
}

// The conversion re-keyed 41 of 121 ids, so every inbound link using the old
// scheme has been 404ing. resolveLegacyID has to recover them without ever
// redirecting to the wrong image.
func TestResolveLegacyID(t *testing.T) {
	withLegacyDB(t)
	ctx := context.Background()

	cases := []struct {
		name, legacy, want string
	}{
		// Re-keyed to the Messier designation -- recovered via the filename stem.
		{"ngc2422 is now m047", "ngc2422", "m047"},
		{"ngc1976 is now m042", "ngc1976", "m042"},
		{"ngc6523 is now m008", "ngc6523", "m008"},

		// Zero-padded -- recovered by either step, both agree.
		{"the id from the production 404", "ngc253b", "ngc0253b"},
		{"padded without suffix", "ngc253", "ngc0253"},
		{"padded to three", "rcw7", "rcw007"},
		{"padded to four", "ic434", "ic0434"},

		// Nothing to recover.
		{"already canonical", "ngc2477", ""},
		{"never existed", "ngc9999", ""},
		{"scanner junk", "wp-admin", ""},
		{"empty", "", ""},

		// Must not resolve to the wrong image.
		{"sh2 prefix is not a catalog number", "sh2-1", ""},
		{"no digits", "moon2", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveLegacyID(ctx, tc.legacy); got != tc.want {
				t.Errorf("resolveLegacyID(%q) = %q, want %q", tc.legacy, got, tc.want)
			}
		})
	}
}

// A legacy id must reach the image as a 301, not a 404 -- that is the whole
// point, and it is what preserves the inbound links and search rankings.
func TestHandleShowRedirectsLegacyID(t *testing.T) {
	withLegacyDB(t)

	for _, tc := range []struct{ legacy, wantLocation string }{
		{"ngc253b", "/show?id=ngc0253b"},
		{"ngc2422", "/show?id=m047"},
		{"rcw7", "/show?id=rcw007"},
	} {
		t.Run(tc.legacy, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com/show?id="+tc.legacy, nil)
			HandleShow(w, r)

			if w.Code != http.StatusMovedPermanently {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusMovedPermanently)
			}
			if got := w.Header().Get("Location"); got != tc.wantLocation {
				t.Errorf("Location = %q, want %q", got, tc.wantLocation)
			}
		})
	}
}

// An unrecoverable id must still 404 rather than redirect somewhere plausible,
// and it must not loop.
func TestHandleShowStillNotFoundForJunk(t *testing.T) {
	withLegacyDB(t)

	for _, id := range []string{"ngc9999", "wp-admin", "../../etc/passwd", "sh2-1"} {
		t.Run(id, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com/show?id="+id, nil)
			HandleShow(w, r)

			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d for id %q, want 404 (Location=%q)", w.Code, id, w.Header().Get("Location"))
			}
		})
	}
}
