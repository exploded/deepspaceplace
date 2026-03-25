package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"

	"deepspaceplace/internal/database"
)

type ShowData struct {
	Image    database.Image
	Prev     string
	Next     string
	Sort     string
	Filter   string
	HasRA    bool
	HasBlink bool
	RAStr    string
	DecStr   string
}

func HandleShow(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	id := r.URL.Query().Get("id")
	sort := r.URL.Query().Get("sort")
	filter := r.URL.Query().Get("filter")

	if id == "" {
		http.NotFound(w, r)
		return
	}

	img, err := DB.GetImage(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			Render(w, "show.html", ShowData{})
			return
		}
		log.Printf("Error fetching image %s: %v", id, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	prev := getPrev(ctx, id, sort)
	next := getNext(ctx, id, sort)

	data := ShowData{
		Image:    img,
		Prev:     prev,
		Next:     next,
		Sort:     sort,
		Filter:   filter,
		HasRA:    img.Ra.Valid && img.Dec.Valid,
		HasBlink: img.Blink != "na" && img.Blink != "",
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

func decimalToHMS(decimal float64) string {
	decimal = decimal / 15.0
	hours := int(decimal)
	minutesDecimal := (decimal - float64(hours)) * 60
	minutes := int(minutesDecimal)
	seconds := (minutesDecimal - float64(minutes)) * 60

	return fmt.Sprintf("%02dh %02dm %02.0fs", hours, minutes, seconds)
}

func decimalToDMS(decimal float64) string {
	sign := "+"
	if decimal < 0 {
		sign = "-"
		decimal = -decimal
	}

	degrees := int(decimal)
	minutesDecimal := (decimal - float64(degrees)) * 60
	minutes := int(minutesDecimal)
	seconds := (minutesDecimal - float64(minutes)) * 60

	return fmt.Sprintf("%s%02d° %02d' %02.0f\"", sign, degrees, minutes, seconds)
}

