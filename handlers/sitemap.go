package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
)

const siteBase = "https://deepspaceplace.com"

var staticPages = []string{
	"/",
	"/images",
	"/skymap",
	"/converter",
	"/moon",
	"/weather",
	"/equipment",
	"/observatory",
	"/links",
	"/timelapse",
	"/terrestrial",
	"/8se",
	"/at8in",
	"/at12in",
	"/ed127",
	"/gso8rc",
	"/mn152",
	"/meteor",
	"/lightpollution",
	"/gso8rcpointing",
	"/gso8rccollimate",
	"/abbreviations",
	"/eq6",
	"/bahtinovmask",
	"/maximdltips",
	"/thermalcamera",
}

func HandleSitemap(w http.ResponseWriter, r *http.Request) {
	images, err := DB.ListAllImages(r.Context())
	if err != nil {
		slog.Error("Error generating sitemap", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?>`)
	fmt.Fprint(w, "\n")
	fmt.Fprint(w, `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	fmt.Fprint(w, "\n")

	for _, page := range staticPages {
		fmt.Fprintf(w, "  <url><loc>%s%s</loc></url>\n", siteBase, page)
	}

	for _, img := range images {
		fmt.Fprintf(w, "  <url><loc>%s/show?id=%s</loc></url>\n", siteBase, img.ID)
	}

	fmt.Fprint(w, "</urlset>\n")
}
