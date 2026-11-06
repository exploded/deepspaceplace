package handlers

import (
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/image/draw"
)

const (
	maxUploadSize = 50 << 20 // 50 MB
	thumbWidth    = 200
	thumbHeight   = 150
	thumbQuality  = 85
)

// uploadTimeout replaces the server's 5-second read and 10-second write
// timeouts for an upload. 50 MB takes about 7 minutes at 1 Mbit/s.
const uploadTimeout = 10 * time.Minute

func HandleAdminUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Both deadlines move: the server starts its write timeout once the
	// headers are in, so a body that takes longer than that loses its
	// response. Errors are ignored: a writer that cannot move its deadlines
	// (an httptest recorder) keeps the server's.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(uploadTimeout))
	_ = rc.SetWriteDeadline(time.Now().Add(uploadTimeout))

	// Parse before the CSRF check, which reads the form: left to that, the
	// body is read without the size cap.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeUploadResult(w, "danger", "File too large (max 50 MB)")
			return
		}
		slog.Info("Upload not received", "error", err)
		writeUploadResult(w, "danger", "Upload failed: the file did not arrive in full")
		return
	}

	if !validateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
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
	// Center-crop the source to 4:3 (200:150) aspect ratio, then scale to 200x150.
	// This preserves aspect ratio without letterboxing.
	bounds := img.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()

	// Target aspect ratio
	targetRatio := float64(thumbWidth) / float64(thumbHeight) // 4:3

	// Calculate the largest 4:3 crop from the center of the source
	var cropW, cropH int
	if float64(srcW)/float64(srcH) > targetRatio {
		// Source is wider than 4:3 — crop sides
		cropH = srcH
		cropW = int(float64(srcH) * targetRatio)
	} else {
		// Source is taller than 4:3 — crop top/bottom
		cropW = srcW
		cropH = int(float64(srcW) / targetRatio)
	}

	x0 := bounds.Min.X + (srcW-cropW)/2
	y0 := bounds.Min.Y + (srcH-cropH)/2
	cropRect := image.Rect(x0, y0, x0+cropW, y0+cropH)

	dst := image.NewRGBA(image.Rect(0, 0, thumbWidth, thumbHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, cropRect, draw.Over, nil)

	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	return jpeg.Encode(out, dst, &jpeg.Options{Quality: thumbQuality})
}
