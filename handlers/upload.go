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
	"strings"

	"golang.org/x/image/draw"
)

const (
	maxUploadSize  = 50 << 20 // 50 MB
	thumbMaxDim    = 400
	thumbQuality   = 85
)

func HandleAdminUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !validateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		writeUploadResult(w, "danger", "File too large (max 50 MB)")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeUploadResult(w, "danger", "No file selected")
		return
	}
	defer file.Close()

	// Validate extension
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		writeUploadResult(w, "danger", "Only .jpg, .jpeg, and .png files are allowed")
		return
	}

	// Sanitise filename: keep only the base name, no path traversal
	baseName := filepath.Base(header.Filename)
	destPath := filepath.Join("images", baseName)

	// Check if file already exists
	if _, err := os.Stat(destPath); err == nil {
		writeUploadResult(w, "warning", fmt.Sprintf("%s already exists — rename the file first", baseName))
		return
	}

	// Decode the image to validate it and generate thumbnail
	img, _, err := image.Decode(file)
	if err != nil {
		writeUploadResult(w, "danger", fmt.Sprintf("Invalid image: %v", err))
		return
	}

	// Save the original — re-encode as JPEG
	out, err := os.Create(destPath)
	if err != nil {
		writeUploadResult(w, "danger", fmt.Sprintf("Save failed: %v", err))
		return
	}
	if err := jpeg.Encode(out, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		out.Close()
		os.Remove(destPath)
		writeUploadResult(w, "danger", fmt.Sprintf("Encode failed: %v", err))
		return
	}
	out.Close()

	// Generate thumbnail
	thumbName := thumbFilename(baseName)
	thumbPath := filepath.Join("images", thumbName)
	if err := generateThumbnail(img, thumbPath); err != nil {
		slog.Error("Thumbnail generation failed", "path", thumbPath, "error", err)
		// Upload still succeeded, just warn
		writeUploadResult(w, "warning", fmt.Sprintf("Uploaded %s but thumbnail failed: %v", baseName, err))
		return
	}

	slog.Info("Image uploaded", "file", baseName, "thumb", thumbName)
	writeUploadResult(w, "success", fmt.Sprintf("Uploaded %s + %s", baseName, thumbName))
}

func writeUploadResult(w http.ResponseWriter, level, msg string) {
	fmt.Fprintf(w, `<div class="alert alert-%s mt-2" role="alert">%s</div>`, level, msg)
}

func thumbFilename(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return base + "_thumb.jpg"
}

func generateThumbnail(img image.Image, path string) error {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	var newW, newH int
	if w >= h {
		newW = thumbMaxDim
		newH = int(float64(h) * float64(thumbMaxDim) / float64(w))
	} else {
		newH = thumbMaxDim
		newW = int(float64(w) * float64(thumbMaxDim) / float64(h))
	}

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, draw.Over, nil)

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	return jpeg.Encode(out, dst, &jpeg.Options{Quality: thumbQuality})
}
