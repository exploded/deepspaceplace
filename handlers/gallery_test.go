package handlers

import (
	"database/sql"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"deepspaceplace/internal/database"

	_ "modernc.org/sqlite"
)

// Every sort value the gallery template emits has to be one the handlers
// accept. "Name" sends sort=ID, which validSorts did not list, so the handler
// 404ed -- and because htmx does not swap a non-2xx response, the button did
// nothing at all rather than visibly failing. Deriving the values from the
// template rather than restating them here is the point: a new sort button
// added to the markup and forgotten in validSorts fails this test.
func TestTemplateSortValuesAreValid(t *testing.T) {
	markup, err := os.ReadFile("../templates/gallery_grid.html")
	if err != nil {
		t.Fatalf("read gallery_grid.html: %v", err)
	}

	// Matches the queryParams call in each sort link: {{queryParams "sort" "ID" ...}}
	re := regexp.MustCompile(`queryParams "sort" "([^"]*)"`)
	matches := re.FindAllStringSubmatch(string(markup), -1)
	if len(matches) == 0 {
		t.Fatal("no literal sort values found in gallery_grid.html -- has the markup changed?")
	}

	seen := map[string]bool{}
	for _, m := range matches {
		value := m[1]
		if seen[value] {
			continue
		}
		seen[value] = true
		if !validSorts[value] {
			t.Errorf("template offers sort=%q, which validSorts rejects (handler would 404)", value)
		}
	}

	// The three buttons are Name, Date and Object Type. If that shrinks, the
	// loop above is asserting less than it looks like it is.
	if len(seen) < 3 {
		t.Errorf("found %d distinct sort values (%v), want at least 3", len(seen), seen)
	}
}

// galleryDB builds a throwaway database whose ids are deliberately out of
// insertion order, so an ordering assertion can tell ORDER BY id from "however
// the rows happened to land".
func galleryDB(t *testing.T) *database.Queries {
	t.Helper()

	db, err := sql.Open("sqlite", "file:gallery?mode=memory&cache=shared")
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

	rows := []struct{ id, name, date, typ string }{
		{"ngc0253", "NGC 253", "2021-09", "Galaxy"},
		{"m008", "M 8", "2023-06", "Nebula"},
		{"m042", "M 42", "2020-01", "Nebula"},
	}
	for _, r := range rows {
		if _, err := db.Exec(
			`INSERT INTO images (id, name, date, type) VALUES (?, ?, ?, ?)`,
			r.id, r.name, r.date, r.typ,
		); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return database.New(db)
}

// withGalleryFixtures swaps in the test database and a stub partial, so the
// gallery handlers can run end to end without the production templates. The
// stub prints the ids in the order the handler produced them.
func withGalleryFixtures(t *testing.T) {
	t.Helper()

	oldDB, oldTemplates := DB, Templates
	DB = galleryDB(t)
	Templates = map[string]*template.Template{
		"gallery.html": template.Must(template.New("gallery.html").Parse(
			`{{define "base"}}{{template "gallery_grid.html" .}}{{end}}` +
				`{{define "gallery_grid.html"}}{{range .Images}}{{.ID}} {{end}}{{end}}`,
		)),
	}
	t.Cleanup(func() { DB, Templates = oldDB, oldTemplates })
}

// sort=ID is what the "Name" button sends. Both the full page and the htmx
// partial must serve it, ordered by id.
func TestGalleryAcceptsNameSort(t *testing.T) {
	withGalleryFixtures(t)

	for _, tc := range []struct {
		name    string
		path    string
		handler http.HandlerFunc
	}{
		{"full page", "/images?sort=ID", HandleGallery},
		{"htmx partial", "/images/partial?sort=ID", HandleGalleryPartial},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			tc.handler(w, httptest.NewRequest(http.MethodGet, "https://deepspaceplace.com"+tc.path, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", w.Code)
			}
			if got, want := strings.Fields(w.Body.String()), []string{"m008", "m042", "ngc0253"}; !equalIDs(got, want) {
				t.Errorf("ids = %v, want %v (ordered by id)", got, want)
			}
		})
	}
}

// sort=ID must also survive the filtered paths, which each have their own
// switch over the sort value.
func TestGalleryNameSortWithFilter(t *testing.T) {
	withGalleryFixtures(t)

	w := httptest.NewRecorder()
	HandleGalleryPartial(w, httptest.NewRequest(http.MethodGet,
		"https://deepspaceplace.com/images/partial?sort=ID&filter=Nebula", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got, want := strings.Fields(w.Body.String()), []string{"m008", "m042"}; !equalIDs(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
}

// Widening validSorts must not turn it into a pass-through: an unknown sort is
// still a 404, not a silent fall back to the default ordering.
func TestGalleryRejectsUnknownSort(t *testing.T) {
	withGalleryFixtures(t)

	for _, sort := range []string{"Name", "id", "name", "date", "id; DROP TABLE images"} {
		t.Run(sort, func(t *testing.T) {
			w := httptest.NewRecorder()
			HandleGalleryPartial(w, httptest.NewRequest(http.MethodGet,
				"https://deepspaceplace.com/images/partial?sort="+url.QueryEscape(sort), nil))

			if w.Code != http.StatusNotFound {
				t.Errorf("status = %d for sort=%q, want 404", w.Code, sort)
			}
		})
	}
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
