package handlers

import (
	"encoding/json"
	"html/template"
	"log/slog"
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
	ctx := r.Context()
	rows, err := DB.ListObservations(ctx)
	if err != nil {
		slog.Error("Error listing observations for skymap", "error", err)
		Render(w, "skymap.html", PageData{CanonicalURL: "https://deepspaceplace.com/skymap", Title: "Sky Map", Description: "Interactive sky map of plate-solved astrophotography observations."})
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

	obsJSON, err := json.Marshal(observations)
	if err != nil {
		slog.Error("Error marshaling observations", "error", err)
		Render(w, "skymap.html", PageData{CanonicalURL: "https://deepspaceplace.com/skymap", Title: "Sky Map", Description: "Interactive sky map of plate-solved astrophotography observations."})
		return
	}

	data := map[string]interface{}{
		"CanonicalURL":     "https://deepspaceplace.com/skymap",
		"Title":            "Sky Map",
		"Description":      "Interactive sky map of plate-solved astrophotography observations.",
		"ObservationsJSON": template.JS(string(obsJSON)),
	}
	Render(w, "skymap.html", data)
}

func HandleObservationsJSON(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := DB.ListObservations(ctx)
	if err != nil {
		slog.Error("Error listing observations", "error", err)
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
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(observations); err != nil {
		slog.Error("Error encoding observations JSON", "error", err)
	}
}
