package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"strings"

	"deepspaceplace/internal/database"
)

type ShowData struct {
	Image        database.Image
	Prev         string
	Next         string
	HasRA        bool
	RAStr        string
	DecStr       string
	CanonicalURL string
	Title        string
	Description  string
	Overlay      *Overlay
}

func HandleShow(w http.ResponseWriter, r *http.Request) {
	if redirectIfEmptyParams(w, r) {
		return
	}
	ctx := r.Context()
	id := r.URL.Query().Get("id")

	// Redirect legacy URLs with sort/filter to clean canonical URL
	if r.URL.Query().Get("sort") != "" || r.URL.Query().Get("filter") != "" {
		http.Redirect(w, r, "/show?id="+url.QueryEscape(id), http.StatusMovedPermanently)
		return
	}

	if id == "" {
		http.NotFound(w, r)
		return
	}

	img, err := DB.GetImage(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			if canonical := resolveLegacyID(ctx, id); canonical != "" {
				http.Redirect(w, r, "/show?id="+url.QueryEscape(canonical), http.StatusMovedPermanently)
				return
			}
			http.NotFound(w, r)
			return
		}
		slog.Error("Error fetching image", "id", id, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	sort, filter := getGalleryPrefs(r)
	prev, next := getFilteredPrevNext(ctx, id, sort, filter)

	data := ShowData{
		Image:        img,
		Prev:         prev,
		Next:         next,
		HasRA:        img.Ra.Valid && img.Dec.Valid,
		CanonicalURL: "https://deepspaceplace.com/show?id=" + id,
		Title:        img.Name,
		Description:  img.Name + " - astrophotography with " + img.Camera + " and " + img.Scope,
	}

	if data.HasRA {
		data.RAStr = decimalToHMS(img.Ra.Float64)
		data.DecStr = decimalToDMS(img.Dec.Float64)
		data.Overlay = BuildOverlay(img)
	}

	Render(w, "show.html", data)
}

// resolveLegacyID maps an id from the old PHP site onto its current row,
// returning "" when there is no match.
//
// The Go conversion re-keyed every image: catalog numbers were zero-padded
// (ngc253b -> ngc0253b, rcw7 -> rcw007, ic434 -> ic0434) and objects with a
// Messier designation were re-keyed to it (ngc2422 -> m047, ngc1976 -> m042).
// 41 of 121 ids changed, so every bookmark, inbound link and search result
// using the old scheme has been 404ing since the conversion.
//
// That went unnoticed for months because those 404s were logged at WARN
// alongside 34,520 scanner hits -- see levelFor in middleware.go, and do not
// try to detect this class from the Referer header again.
//
// Both steps verify the candidate against the primary key before redirecting,
// so an id that resolves to nothing (sh2-280, n70) falls through to a 404
// rather than guessing.
func resolveLegacyID(ctx context.Context, id string) string {
	if id == "" || len(id) > 64 {
		return ""
	}

	// The filename column still carries the old id -- m047 is ngc2422.jpg --
	// so the stem is an exact reverse mapping wherever an image was re-keyed.
	canonical, err := DB.GetImageIDByFilenameStem(ctx, id)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("Error resolving legacy image id by filename", "id", id, "error", err)
		return ""
	}
	if err == nil && canonical != id {
		return canonical
	}

	// Otherwise the only change was zero-padding. Widths come from the data
	// rather than a per-prefix guess: widen until one candidate is a real id.
	prefix, digits, suffix, ok := splitCatalogID(id)
	if !ok {
		return ""
	}
	for width := len(digits) + 1; width <= 5; width++ {
		candidate := prefix + strings.Repeat("0", width-len(digits)) + digits + suffix
		if _, err := DB.GetImage(ctx, candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// splitCatalogID splits an id like "ngc253b" into "ngc", "253" and "b". It
// reports false when the id is not prefix-digits-suffix shaped.
func splitCatalogID(id string) (prefix, digits, suffix string, ok bool) {
	i := 0
	for i < len(id) && (id[i] < '0' || id[i] > '9') {
		i++
	}
	j := i
	for j < len(id) && id[j] >= '0' && id[j] <= '9' {
		j++
	}
	if i == 0 || j == i {
		return "", "", "", false
	}
	return id[:i], id[i:j], id[j:], true
}

func getFilteredPrevNext(ctx context.Context, id, sort, filter string) (prev, next string) {
	filterScope, filterCamera, filterType := resolveFilter(filter)
	images, err := listFiltered(ctx, sort, filter, filterType, filterCamera, filterScope, 10000, 0)
	if err != nil {
		slog.Error("Error listing images for prev/next", "error", err)
		return "", ""
	}
	for i, img := range images {
		if img.ID == id {
			if i > 0 {
				prev = images[i-1].ID
			}
			if i < len(images)-1 {
				next = images[i+1].ID
			}
			return prev, next
		}
	}
	return "", ""
}

// splitHMS splits a decimal RA (degrees) into hours, minutes, and seconds.
func splitHMS(decimal float64) (h, m int, s float64) {
	decimal = decimal / 15.0
	h = int(decimal)
	minDec := (decimal - float64(h)) * 60
	m = int(minDec)
	s = (minDec - float64(m)) * 60
	h, m, s = carrySexagesimal(h, m, s, 24)
	return
}

// splitDMS splits a decimal angle into sign, degrees, minutes, and seconds.
func splitDMS(decimal float64) (sign string, d, m int, s float64) {
	sign = "+"
	if decimal < 0 {
		sign = "-"
		decimal = -decimal
	}
	d = int(decimal)
	minDec := (decimal - float64(d)) * 60
	m = int(minDec)
	s = (minDec - float64(m)) * 60
	d, m, s = carrySexagesimal(d, m, s, 0)
	return
}

// carrySexagesimal propagates the carry that displaying seconds to the nearest
// whole number creates. Both formatters below round seconds with %02.0f, so a
// value like 59.7 becomes "60" and produces readings such as "03h 46m 60s"
// instead of "03h 47m 00s".
//
// wrap is the value the leading unit rolls over at -- 24 for hours, or 0 to
// leave degrees unbounded, since a declination can never carry past 90 anyway.
func carrySexagesimal(big, min int, sec float64, wrap int) (int, int, float64) {
	if math.Round(sec) < 60 {
		return big, min, sec
	}
	sec = 0
	min++
	if min >= 60 {
		min = 0
		big++
		if wrap > 0 && big >= wrap {
			big -= wrap
		}
	}
	return big, min, sec
}

func decimalToHMS(decimal float64) string {
	h, m, s := splitHMS(decimal)
	return fmt.Sprintf("%02dh %02dm %02.0fs", h, m, s)
}

func decimalToDMS(decimal float64) string {
	sign, d, m, s := splitDMS(decimal)
	return fmt.Sprintf("%s%02d° %02d' %02.0f\"", sign, d, m, s)
}
