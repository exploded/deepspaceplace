package handlers

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// slowReader hands out its data a chunk at a time with a pause before each,
// like a browser uploading over a slow link.
type slowReader struct {
	r     io.Reader
	chunk int
	pause time.Duration
}

func (s *slowReader) Read(p []byte) (int, error) {
	time.Sleep(s.pause)
	if len(p) > s.chunk {
		p = p[:s.chunk]
	}
	return s.r.Read(p)
}

// uploadForm builds the multipart body the admin page's upload form sends.
func uploadForm(t *testing.T, filename string) (body []byte, contentType string) {
	t.Helper()
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 40, 30))); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf_token", "test-token")
	fw, err := mw.CreateFormFile("image", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(img.Bytes())
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

// An upload that outlasts the server's read and write timeouts must still be
// read in full and answered: the server's limits suit page requests, not a
// photo arriving over a slow connection.
func TestUploadOutlastsServerTimeouts(t *testing.T) {
	withCSRFToken(t)
	t.Chdir(t.TempDir())
	if err := os.Mkdir("images", 0o755); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(http.HandlerFunc(HandleAdminUpload))
	srv.Config.ReadTimeout = 100 * time.Millisecond
	srv.Config.WriteTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()

	body, contentType := uploadForm(t, "slow.png")
	// Ten chunks 50 ms apart: about 500 ms, well past both timeouts.
	slow := &slowReader{r: bytes.NewReader(body), chunk: len(body)/10 + 1, pause: 50 * time.Millisecond}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/upload", slow)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", contentType)

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(got), "alert-success") {
		t.Fatalf("status %d, body %q; want the success banner", resp.StatusCode, got)
	}
	for _, name := range []string{"slow.png", "slow_thumb.jpg"} {
		if _, err := os.Stat(filepath.Join("images", name)); err != nil {
			t.Errorf("%s not saved: %v", name, err)
		}
	}
}

// A file over the cap is refused as too large, not misreported as a bad
// form or a missing CSRF token.
func TestUploadRefusesOversizedFile(t *testing.T) {
	withCSRFToken(t)
	t.Chdir(t.TempDir())

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("csrf_token", "test-token")
	fw, _ := mw.CreateFormFile("image", "huge.png")
	fw.Write(make([]byte, maxUploadSize+1))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/admin/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	HandleAdminUpload(rec, req)

	if body := rec.Body.String(); !strings.Contains(body, "File too large") {
		t.Fatalf("status %d, body %q; want the too-large banner", rec.Code, body)
	}
}
