package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"

	"deepspaceplace/internal/database"
)

type ShowData struct {
	Image        database.Image
	Prev         string
	Next         string
	Sort         string
	Filter       string
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
	sort := r.URL.Query().Get("sort")
	filter := r.URL.Query().Get("filter")

	if id == "" || !validSorts[sort] || !validFilters[filter] {
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

	prev := getPrev(ctx, id, sort)
	next := getNext(ctx, id, sort)

	data := ShowData{
		Image:        img,
		Prev:         prev,
		Next:         next,
		Sort:         sort,
		Filter:       filter,
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

func getPrev(ctx context.Context, id, sort string) string {
	var prevID string
	var err error

	switch sort {
	case "Date":
		prevID, err = DB.GetPrevByDate(ctx, database.GetPrevByDateParams{ID: id, ID_2: id, ID_3: id})
	case "Type":
		prevID, err = DB.GetPrevByType(ctx, database.GetPrevByTypeParams{ID: id, ID_2: id, ID_3: id})
	default:
		prevID, err = DB.GetPrevByID(ctx, id)
	}

	if err != nil {
		return ""
	}
	return prevID
}

func getNext(ctx context.Context, id, sort string) string {
	var nextID string
	var err error

	switch sort {
	case "Date":
		nextID, err = DB.GetNextByDate(ctx, database.GetNextByDateParams{ID: id, ID_2: id, ID_3: id})
	case "Type":
		nextID, err = DB.GetNextByType(ctx, database.GetNextByTypeParams{ID: id, ID_2: id, ID_3: id})
	default:
		nextID, err = DB.GetNextByID(ctx, id)
	}

	if err != nil {
		return ""
	}
	return nextID
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

