package handlers

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func withStaticDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "css"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := StaticDir
	StaticDir = dir
	t.Cleanup(func() {
		StaticDir = old
		assetVersions.Range(func(k, _ any) bool { assetVersions.Delete(k); return true })
	})
	return dir
}

// The point of the whole exercise: edit a stylesheet, get a different URL.
// Same bytes must keep the same URL, or every deploy would bust the cache for
// files that did not change.
func TestAssetURLTracksContents(t *testing.T) {
	dir := withStaticDir(t)
	css := filepath.Join(dir, "css", "dsp.css")

	if err := os.WriteFile(css, []byte(".imageBox { position: relative; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	first := assetURL("/static/css/dsp.css")

	if !strings.HasPrefix(first, "/static/css/dsp.css?v=") {
		t.Fatalf("got %q, want the path with a version query", first)
	}

	// Same bytes, fresh cache: same URL.
	assetVersions.Delete("/static/css/dsp.css")
	if again := assetURL("/static/css/dsp.css"); again != first {
		t.Errorf("unchanged file changed URL: %q then %q", first, again)
	}

	// Edited: different URL, or the fix never reaches a returning visitor.
	if err := os.WriteFile(css, []byte(".imageBox { width: fit-content; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	assetVersions.Delete("/static/css/dsp.css")
	if edited := assetURL("/static/css/dsp.css"); edited == first {
		t.Errorf("edited file kept URL %q; it would stay cached for a week", edited)
	}
}

// A missing asset must still render a usable link. An unversioned stylesheet is
// stale for a week; a broken href is a page with no styling at all.
func TestAssetURLFallsBackToBarePath(t *testing.T) {
	withStaticDir(t)
	if got := assetURL("/static/css/absent.css"); got != "/static/css/absent.css" {
		t.Errorf("got %q, want the bare path", got)
	}
}

func TestAssetURLRefusesTraversalAndForeignPaths(t *testing.T) {
	dir := withStaticDir(t)
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/static/../secret.txt",
		"/static/css/../../secret.txt",
		"/images/lovejoy2.jpg", // not under /static/, nothing to hash
		"/favicon.ico",
	} {
		if got := assetURL(p); got != p {
			t.Errorf("assetURL(%q) = %q, want it returned untouched", p, got)
		}
	}
}

// End to end through the real base.html and the real static directory: the
// unit tests above prove assetURL hashes, this proves it is actually wired into
// the page. A missing entry in TemplateFuncs would fail parsing outright, but a
// template that quietly stopped calling it would not.
func TestBaseTemplateEmitsVersionedStylesheet(t *testing.T) {
	old := StaticDir
	StaticDir = filepath.Join("..", "static")
	t.Cleanup(func() {
		StaticDir = old
		assetVersions.Range(func(k, _ any) bool { assetVersions.Delete(k); return true })
	})
	assetVersions.Range(func(k, _ any) bool { assetVersions.Delete(k); return true })

	tmpl, err := template.New("base.html").Funcs(TemplateFuncs).
		ParseFiles(filepath.Join("..", "templates", "base.html"))
	if err != nil {
		t.Fatalf("parse base.html: %v", err)
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "base", PageData{Title: "Test"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := buf.String()

	re := regexp.MustCompile(`/static/css/dsp\.css\?v=[0-9a-f]{10}`)
	if !re.MatchString(body) {
		t.Errorf("base.html did not emit a versioned dsp.css\ngot: %s",
			regexp.MustCompile(`(?m)^.*dsp\.css.*$`).FindString(body))
	}
}

// Every stylesheet and script in the templates must go through asset, or the
// immutable header quietly pins it for a week. Photographs are exempt: their
// bytes never change under a given name, which is what immutable is for.
func TestTemplatesVersionTheirStylesheetsAndScripts(t *testing.T) {
	var offenders []string

	err := filepath.WalkDir("../templates", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, bare := range []string{`href="/static/`, `src="/static/js/`} {
			for _, line := range strings.Split(string(body), "\n") {
				if !strings.Contains(line, bare) {
					continue
				}
				// Icons and the manifest are referenced by fixed name and are
				// not code; only stylesheets and scripts matter here.
				if strings.Contains(line, ".css") || strings.Contains(line, ".js") {
					offenders = append(offenders, filepath.ToSlash(p)+": "+strings.TrimSpace(line))
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, o := range offenders {
		t.Errorf("unversioned asset, will stay cached for a week after an edit\n  %s", o)
	}
}
