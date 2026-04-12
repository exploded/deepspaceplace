package handlers

import (
	"fmt"
	"html/template"
	"image"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

const (
	defaultMaxDimension = 3840
	jpegQuality         = 90
)

type ResizeResult struct {
	Filename   string
	OldW, OldH int
	NewW, NewH int
	Status     string // "resized", "skipped", "error"
	Error      string
}

type ResizeData struct {
	PageData
	Results []ResizeResult
	Resized int
	Skipped int
	Errors  int
	Total   int
	Done    bool
	MaxDim  int
}

func HandleAdminResize(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		if !validateCSRF(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		rc := http.NewResponseController(w)
		rc.SetWriteDeadline(time.Now().Add(10 * time.Minute))

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		maxDim := defaultMaxDimension
		if v := r.FormValue("max_dimension"); v != "" {
			if d, err := strconv.Atoi(v); err == nil && d >= 800 && d <= 7680 {
				maxDim = d
			}
		}

		// Stream the page header so the connection stays alive during resize.
		// Render the base template up to the content block, then stream rows.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		// Write the page shell (everything before the dynamic content).
		fmt.Fprint(w, `<!doctype html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link rel="icon" type="image/svg+xml" href="/static/favicon.svg">
    <link href="/static/css/bootstrap.css" rel="stylesheet">
    <link href="/static/css/dsp.css" rel="stylesheet">
    <title>Resize Images - Deep Space Place</title>
</head>
<body>
<script src="/static/js/bootstrap.bundle.js"></script>
<div class="container">
<h1>Admin - Resize Images</h1>
<div class="mb-3"><strong>Processing images (max `)
		fmt.Fprintf(w, "%dpx)...</strong></div>\n", maxDim)
		fmt.Fprint(w, `<table class="table table-sm table-striped">
<thead><tr><th>File</th><th>Original</th><th>Result</th><th>Status</th></tr></thead>
<tbody>
`)
		flusher.Flush()

		// Process images one at a time, streaming each result row.
		var resized, skipped, errors, total int
		filepath.Walk("images", func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			lower := strings.ToLower(info.Name())
			if strings.Contains(lower, "_thumb") {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(info.Name()))
			if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
				return nil
			}

			result := resizeImage(path, maxDim)
			total++

			var badge, detail string
			switch result.Status {
			case "resized":
				resized++
				badge = `<span class="badge bg-success">resized</span>`
				detail = fmt.Sprintf("%d x %d", result.NewW, result.NewH)
				slog.Info("Resized image", "path", path,
					"from", fmt.Sprintf("%dx%d", result.OldW, result.OldH),
					"to", fmt.Sprintf("%dx%d", result.NewW, result.NewH))
			case "skipped":
				skipped++
				badge = `<span class="badge bg-secondary">skipped</span>`
				detail = fmt.Sprintf("%d x %d (ok)", result.NewW, result.NewH)
			case "error":
				errors++
				badge = `<span class="badge bg-danger">error</span>`
				detail = result.Error
			}

			fmt.Fprintf(w, "<tr><td>%s</td><td>%d x %d</td><td>%s</td><td>%s</td></tr>\n",
				template.HTMLEscapeString(filepath.ToSlash(result.Filename)),
				result.OldW, result.OldH, detail, badge)
			flusher.Flush()
			return nil
		})

		// Write summary and close the page.
		fmt.Fprint(w, "</tbody></table>\n")
		fmt.Fprintf(w, `<div class="alert alert-info">
    <strong>Done:</strong> %d images scanned —
    %d resized, %d already OK, %d errors.
    Max dimension: %dpx.
</div>
<a href="/admin" class="btn btn-primary mt-3">Back to Admin</a>
</div></body></html>`, total, resized, skipped, errors, maxDim)
		flusher.Flush()
		return
	}

	type resizePageData struct {
		ResizeData
		CSRFToken string
	}
	Render(w, "resize.html", resizePageData{ResizeData: ResizeData{MaxDim: defaultMaxDimension}, CSRFToken: getCSRFToken()})
}

func resizeImage(path string, maxDim int) ResizeResult {
	result := ResizeResult{Filename: filepath.ToSlash(path)}

	f, err := os.Open(path)
	if err != nil {
		result.Status = "error"
		result.Error = err.Error()
		return result
	}

	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("decode: %v", err)
		return result
	}

	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	result.OldW, result.OldH = w, h

	if w <= maxDim && h <= maxDim {
		result.Status = "skipped"
		result.NewW, result.NewH = w, h
		return result
	}

	// Calculate new dimensions preserving aspect ratio
	var newW, newH int
	if w >= h {
		newW = maxDim
		newH = int(float64(h) * float64(maxDim) / float64(w))
	} else {
		newH = maxDim
		newW = int(float64(w) * float64(maxDim) / float64(h))
	}
	result.NewW, result.NewH = newW, newH

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	out, err := os.Create(path)
	if err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("create: %v", err)
		return result
	}
	defer out.Close()

	if err := jpeg.Encode(out, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		result.Status = "error"
		result.Error = fmt.Sprintf("encode: %v", err)
		return result
	}

	result.Status = "resized"
	return result
}
