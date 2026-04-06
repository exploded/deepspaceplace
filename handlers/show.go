package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"deepspaceplace/internal/database"
)

type ShowData struct {
	Image        database.Image
	Prev         string
	Next         string
	HasRA        bool
	HasBlink     bool
	RAStr        string
	DecStr       string
	CanonicalURL string
	Title        string
	Description  string
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
		HasBlink:     img.Blink != "na" && img.Blink != "",
		CanonicalURL: "https://deepspaceplace.com/show?id=" + id,
		Title:        img.Name,
		Description:  img.Name + " - astrophotography with " + img.Camera + " and " + img.Scope,
	}

	if data.HasRA {
		data.RAStr = decimalToHMS(img.Ra.Float64)
		data.DecStr = decimalToDMS(img.Dec.Float64)
	}

	Render(w, "show.html", data)
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
	return
}

func decimalToHMS(decimal float64) string {
	h, m, s := splitHMS(decimal)
	return fmt.Sprintf("%02dh %02dm %02.0fs", h, m, s)
}

func decimalToDMS(decimal float64) string {
	sign, d, m, s := splitDMS(decimal)
	return fmt.Sprintf("%s%02d° %02d' %02.0f\"", sign, d, m, s)
}

