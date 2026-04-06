package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"deepspaceplace/internal/database"
)

const maxPerPage = 120

var validSorts = map[string]bool{
	"":     true,
	"Date": true,
	"Type": true,
}

var validFilters = map[string]bool{
	"":          true,
	"all":       true,
	"new":       true,
	"TOA130NS":  true,
	"ED127":     true,
	"GSO8RC":    true,
	"AT8IN":     true,
	"AT12IN":    true,
	"MN152":     true,
	"STL":       true,
	"QHY9":      true,
	"500D":      true,
	"D50":       true,
	"ASI2600MM": true,
	"Galaxy":    true,
	"Nebula":    true,
	"Globular":  true,
	"Cluster":   true,
}

type GalleryData struct {
	Images       []database.Image
	Sort         string
	Filter       string
	Page         int
	TotalPages   int
	TotalRows    int
	CanonicalURL string
	Title        string
	Description  string
}

func HandleGallery(w http.ResponseWriter, r *http.Request) {
	if redirectIfEmptyParams(w, r) {
		return
	}
	if !validSorts[r.URL.Query().Get("sort")] || !validFilters[r.URL.Query().Get("filter")] {
		http.NotFound(w, r)
		return
	}
	setGalleryPrefsCookie(w, r.URL.Query().Get("sort"), r.URL.Query().Get("filter"))
	data := buildGalleryData(r)
	Render(w, "gallery.html", data)
}

func HandleGalleryPartial(w http.ResponseWriter, r *http.Request) {
	if redirectIfEmptyParams(w, r) {
		return
	}
	if !validSorts[r.URL.Query().Get("sort")] || !validFilters[r.URL.Query().Get("filter")] {
		http.NotFound(w, r)
		return
	}
	setGalleryPrefsCookie(w, r.URL.Query().Get("sort"), r.URL.Query().Get("filter"))
	data := buildGalleryData(r)
	RenderPartial(w, "gallery.html", "gallery_grid.html", data)
}

func buildGalleryData(r *http.Request) GalleryData {
	ctx := r.Context()
	sort := r.URL.Query().Get("sort")
	filter := r.URL.Query().Get("filter")
	page, _ := strconv.Atoi(r.URL.Query().Get("pageNum_rsImages"))
	if page < 0 {
		page = 0
	}

	offset := int64(page * maxPerPage)
	limit := int64(maxPerPage)

	var images []database.Image
	var totalRows int64
	var err error

	// Get filter values
	filterScope, filterCamera, filterType := resolveFilter(filter)

	// Count total
	totalRows, err = countFiltered(ctx, filter, filterType, filterCamera, filterScope)
	if err != nil {
		slog.Error("Error counting images", "error", err)
	}

	// Fetch images
	images, err = listFiltered(ctx, sort, filter, filterType, filterCamera, filterScope, limit, offset)
	if err != nil {
		slog.Error("Error listing images", "error", err)
	}

	totalPages := 0
	if totalRows > 0 {
		totalPages = int((totalRows - 1) / maxPerPage)
	}

	canonical := "https://deepspaceplace.com/images"
	q := url.Values{}
	if sort != "" {
		q.Set("sort", sort)
	}
	if filter != "" {
		q.Set("filter", filter)
	}
	if page > 0 {
		q.Set("pageNum_rsImages", strconv.Itoa(page))
	}
	if len(q) > 0 {
		canonical += "?" + q.Encode()
	}

	return GalleryData{
		Images:       images,
		Sort:         sort,
		Filter:       filter,
		Page:         page,
		TotalPages:   totalPages,
		TotalRows:    int(totalRows),
		CanonicalURL: canonical,
		Title:        "Deep Space Gallery",
		Description:  "Astrophotography gallery of deep space objects including galaxies, nebulae, and star clusters.",
	}
}

func resolveFilter(filter string) (scope, camera, objType string) {
	switch filter {
	case "TOA130NS":
		return "TOA130NS", "", ""
	case "ED127":
		return "ED127", "", ""
	case "GSO8RC":
		return "GSO 8 RC", "", ""
	case "AT8IN":
		return "AT8 IN", "", ""
	case "AT12IN":
		return "AT12 IN", "", ""
	case "MN152":
		return "MN152 F4.8 Mak-Newt", "", ""
	case "STL":
		return "", "STL-11000M", ""
	case "QHY9":
		return "", "QHY9", ""
	case "500D":
		return "", "Canon 500D", ""
	case "D50":
		return "", "Nikon D50", ""
	case "ASI2600MM":
		return "", "ASI2600MM DUO", ""
	case "Galaxy":
		return "", "", "Galaxy"
	case "Nebula":
		return "", "", "Nebula"
	case "Globular":
		return "", "", "Globular Cluster"
	case "Cluster":
		return "", "", "Cluster"
	default:
		return "", "", ""
	}
}

func countFiltered(ctx context.Context, filter, objType, camera, scope string) (int64, error) {
	switch {
	case filter == "new":
		cutoff := time.Now().AddDate(-1, 0, 0).Format("2006-01")
		return DB.CountImagesNew(ctx, cutoff)
	case objType != "":
		return DB.CountImagesByType(ctx, objType)
	case camera != "":
		return DB.CountImagesByCamera(ctx, camera)
	case scope != "":
		return DB.CountImagesByScope(ctx, scope)
	default:
		return DB.CountImages(ctx)
	}
}

func listFiltered(ctx context.Context, sort, filter, objType, camera, scope string, limit, offset int64) ([]database.Image, error) {
	switch {
	case filter == "new":
		cutoff := time.Now().AddDate(-1, 0, 0).Format("2006-01")
		return DB.ListImagesByDateFilterNew(ctx, database.ListImagesByDateFilterNewParams{
			Date:   cutoff,
			Limit:  limit,
			Offset: offset,
		})
	case objType != "":
		return listByTypeFilter(ctx, sort, objType, limit, offset)
	case camera != "":
		return listByCameraFilter(ctx, sort, camera, limit, offset)
	case scope != "":
		return listByScopeFilter(ctx, sort, scope, limit, offset)
	default:
		return listUnfiltered(ctx, sort, limit, offset)
	}
}

func listUnfiltered(ctx context.Context, sort string, limit, offset int64) ([]database.Image, error) {
	switch sort {
	case "Date":
		return DB.ListImagesByDateDesc(ctx, database.ListImagesByDateDescParams{Limit: limit, Offset: offset})
	case "Type":
		return DB.ListImagesByType(ctx, database.ListImagesByTypeParams{Limit: limit, Offset: offset})
	default:
		return DB.ListImagesByID(ctx, database.ListImagesByIDParams{Limit: limit, Offset: offset})
	}
}

func listByTypeFilter(ctx context.Context, sort, objType string, limit, offset int64) ([]database.Image, error) {
	switch sort {
	case "Date":
		return DB.ListImagesByDateFilterType(ctx, database.ListImagesByDateFilterTypeParams{Type: objType, Limit: limit, Offset: offset})
	case "Type":
		return DB.ListImagesByTypeFilterType(ctx, database.ListImagesByTypeFilterTypeParams{Type: objType, Limit: limit, Offset: offset})
	default:
		return DB.ListImagesByIDFilterType(ctx, database.ListImagesByIDFilterTypeParams{Type: objType, Limit: limit, Offset: offset})
	}
}

func listByCameraFilter(ctx context.Context, sort, camera string, limit, offset int64) ([]database.Image, error) {
	switch sort {
	case "Date":
		return DB.ListImagesByDateFilterCamera(ctx, database.ListImagesByDateFilterCameraParams{Camera: camera, Limit: limit, Offset: offset})
	default:
		return DB.ListImagesByIDFilterCamera(ctx, database.ListImagesByIDFilterCameraParams{Camera: camera, Limit: limit, Offset: offset})
	}
}

func listByScopeFilter(ctx context.Context, sort, scope string, limit, offset int64) ([]database.Image, error) {
	switch sort {
	case "Date":
		return DB.ListImagesByDateFilterScope(ctx, database.ListImagesByDateFilterScopeParams{Scope: scope, Limit: limit, Offset: offset})
	default:
		return DB.ListImagesByIDFilterScope(ctx, database.ListImagesByIDFilterScopeParams{Scope: scope, Limit: limit, Offset: offset})
	}
}

func setGalleryPrefsCookie(w http.ResponseWriter, sort, filter string) {
	v := url.Values{}
	if sort != "" {
		v.Set("sort", sort)
	}
	if filter != "" {
		v.Set("filter", filter)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "gallery_prefs",
		Value:    v.Encode(),
		Path:     "/",
		MaxAge:   2592000, // 30 days
		HttpOnly: true,
		Secure:   Prod,
		SameSite: http.SameSiteLaxMode,
	})
}

func getGalleryPrefs(r *http.Request) (string, string) {
	c, err := r.Cookie("gallery_prefs")
	if err != nil {
		return "", ""
	}
	v, err := url.ParseQuery(c.Value)
	if err != nil {
		return "", ""
	}
	sort := v.Get("sort")
	filter := v.Get("filter")
	if !validSorts[sort] {
		sort = ""
	}
	if !validFilters[filter] {
		filter = ""
	}
	return sort, filter
}
