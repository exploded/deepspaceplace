package handlers

import (
	"context"
	"database/sql"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// withAdminEdit sets the CSRF token the posts carry and stubs edit.html down to
// the error banner, so a refused save can be checked without the real layout.
func withAdminEdit(t *testing.T) {
	t.Helper()

	adminSessionMu.Lock()
	prevToken := adminCSRFToken
	adminCSRFToken = "test-token"
	adminSessionMu.Unlock()

	oldTemplates := Templates
	Templates = map[string]*template.Template{
		"edit.html": template.Must(template.New("edit.html").Parse(
			`{{define "base"}}{{.ID}}: {{.Error}}{{end}}`,
		)),
	}

	t.Cleanup(func() {
		adminSessionMu.Lock()
		adminCSRFToken = prevToken
		adminSessionMu.Unlock()
		Templates = oldTemplates
	})
}

func postEdit(t *testing.T, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf_token", "test-token")
	req := httptest.NewRequest(http.MethodPost, "/admin/edit", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	HandleAdminEdit(rec, req)
	return rec
}

func imageExists(t *testing.T, id string) bool {
	t.Helper()
	_, err := DB.GetImage(context.Background(), id)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("read %s: %v", id, err)
	}
	return err == nil
}

func TestEditRenamesImage(t *testing.T) {
	id := withTestDB(t)
	withAdminEdit(t)

	rec := postEdit(t, url.Values{"id": {"comet-lovejoy"}, "id_orig": {id}, "name": {"Lovejoy"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body %q", rec.Code, rec.Body.String())
	}

	if imageExists(t, id) {
		t.Errorf("%s still exists after the rename", id)
	}
	if img := readRow(t, "comet-lovejoy"); img.Name != "Lovejoy" {
		t.Errorf("name = %q, want the edit saved under the new id", img.Name)
	}
}

// A refused rename must leave the row exactly as it was: the other field
// edits on the same form are not saved under the old id either.
func TestEditRenameRefused(t *testing.T) {
	cases := []struct {
		name     string
		newID    string
		setup    string
		wantCode int
		wantMsg  string
	}{
		{"onto an existing id", "m016", `INSERT INTO images (id) VALUES ('m016')`,
			http.StatusConflict, "already has the ID"},
		{"while a solve is running", "comet-lovejoy", `UPDATE images SET solved = 'p', solve_subid = 1`,
			http.StatusConflict, "plate solve is running"},
		{"to an id with a space", "comet lovejoy", "", http.StatusBadRequest, "IDs use only"},
		{"to an id with a slash", "comet/lovejoy", "", http.StatusBadRequest, "IDs use only"},
		{"to an empty id", "", "", http.StatusBadRequest, "IDs use only"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := withTestDB(t)
			withAdminEdit(t)
			if tc.setup != "" {
				if _, err := sqlConn(t).Exec(tc.setup); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}

			rec := postEdit(t, url.Values{"id": {tc.newID}, "id_orig": {id}, "name": {"Lovejoy"}})
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if body := rec.Body.String(); !strings.Contains(body, tc.wantMsg) {
				t.Errorf("body %q does not explain the refusal (%q)", body, tc.wantMsg)
			}

			if img := readRow(t, id); img.Name != "" {
				t.Errorf("name = %q, want the row left untouched", img.Name)
			}
			if tc.newID != "" && tc.setup == "" && imageExists(t, tc.newID) {
				t.Errorf("%s was created by a refused rename", tc.newID)
			}
		})
	}
}

// sqlConn opens a second handle on withTestDB's shared in-memory database, for
// test setup the generated queries don't cover.
func sqlConn(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestValidImageID(t *testing.T) {
	for _, id := range []string{"ngc0253b", "sh2-280", "lovejoy1", "m042", "ic_2944", "a.b"} {
		if !validImageID(id) {
			t.Errorf("validImageID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", "a b", "a/b", "a?b", "a&b", "a%20b", strings.Repeat("x", 65)} {
		if validImageID(id) {
			t.Errorf("validImageID(%q) = true, want false", id)
		}
	}
}
