package handlers

import (
	"fmt"
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

		maxDim := defaultMaxDimension
		if v := r.FormValue("max_dimension"); v != "" {
			if d, err := strconv.Atoi(v); err == nil && d >= 800 && d <= 7680 {
				maxDim = d
			}
		}

		results := resizeAllImages("images", maxDim)

		data := ResizeData{
			Results: results,
			Done:    true,
			MaxDim:  maxDim,
		}
		for _, res := range results {
			switch res.Status {
			case "resized":
				data.Resized++
			case "skipped":
				data.Skipped++
			case "error":
				data.Errors++
			}
		}
		data.Total = len(results)

		Render(w, "resize.html", data)
		return
	}

	type resizePageData struct {
		ResizeData
		CSRFToken string
	}
	Render(w, "resize.html", resizePageData{ResizeData: ResizeData{MaxDim: defaultMaxDimension}, CSRFToken: getCSRFToken()})
}

func resizeAllImages(dir string, maxDim int) []ResizeResult {
	var results []ResizeResult

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
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
		results = append(results, result)
		if result.Status == "resized" {
			slog.Info("Resized image", "path", path, "from", fmt.Sprintf("%dx%d", result.OldW, result.OldH), "to", fmt.Sprintf("%dx%d", result.NewW, result.NewH))
		}
		return nil
	})

	return results
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
