package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

type Observation struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Thumbnail   string  `json:"thumbnail"`
	RA          float64 `json:"ra"`
	Dec         float64 `json:"dec"`
	FieldW      float64 `json:"fieldw"`
	FieldH      float64 `json:"fieldh"`
	Orientation float64 `json:"orientation"`
}

func HandleSkymap(w http.ResponseWriter, r *http.Request) {
	Render(w, "skymap.html", nil)
}

func HandleObservationsJSON(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	rows, err := DB.ListObservations(ctx)
	if err != nil {
		log.Printf("Error listing observations: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	var observations []Observation
	for _, row := range rows {
		observations = append(observations, Observation{
			ID:          row.ID,
			Name:        row.Name,
			Thumbnail:   row.Thumbnail,
			RA:          row.Ra.Float64,
			Dec:         row.Dec.Float64,
			FieldW:      row.Fieldw.Float64,
			FieldH:      row.Fieldh.Float64,
			Orientation: row.Orientation.Float64,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(observations)
}
