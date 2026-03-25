package handlers

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"deepspaceplace/internal/database"
)

var (
	adminSessionToken string
	adminSessionMu    sync.Mutex
)

func adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err == nil && cookie.Value != "" {
			adminSessionMu.Lock()
			valid := cookie.Value == adminSessionToken && adminSessionToken != ""
			adminSessionMu.Unlock()
			if valid {
				next(w, r)
				return
			}
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	}
}

func HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()
		password := r.FormValue("password")

		adminPass := os.Getenv("ADMIN_PASSWORD")
		if adminPass == "" {
			adminPass = "admin" // dev default
		}

		if password == adminPass {
			token := generateToken()
			adminSessionMu.Lock()
			adminSessionToken = token
			adminSessionMu.Unlock()

			http.SetCookie(w, &http.Cookie{
				Name:     "admin_session",
				Value:    token,
				Path:     "/admin",
				HttpOnly: true,
			})
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		Render(w, "login.html", "Invalid password")
		return
	}

	Render(w, "login.html", nil)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func HandleAdmin(w http.ResponseWriter, r *http.Request) {
	adminAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		images, err := DB.ListAllImages(ctx)
		if err != nil {
			log.Printf("Error listing images: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		Render(w, "list.html", images)
	})(w, r)
}

func HandleAdminEdit(w http.ResponseWriter, r *http.Request) {
	adminAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		if r.Method == http.MethodPost {
			r.ParseForm()
			params := database.UpdateImageParams{
				Archive:    r.FormValue("archive"),
				Messier:    r.FormValue("messier"),
				Ngc:        r.FormValue("ngc"),
				Ic:         r.FormValue("ic"),
				Rcw:        r.FormValue("rcw"),
				Sh2:        r.FormValue("sh2"),
				Henize:     r.FormValue("henize"),
				Gum:        r.FormValue("gum"),
				Lbn:        r.FormValue("lbn"),
				CommonName: r.FormValue("common_name"),
				Name:       r.FormValue("name"),
				Filename:   r.FormValue("filename"),
				Thumbnail:  r.FormValue("thumbnail"),
				Type:       r.FormValue("type"),
				Camera:     r.FormValue("camera"),
				Scope:      r.FormValue("scope"),
				Mount:      r.FormValue("mount"),
				Guiding:    r.FormValue("guiding"),
				Exposure:   r.FormValue("exposure"),
				Location:   r.FormValue("location"),
				Date:       r.FormValue("date"),
				Notes:      r.FormValue("notes"),
				Blink:      r.FormValue("blink"),
				Corrector:  r.FormValue("corrector"),
				Solved:     r.FormValue("solved"),
				ID:         r.FormValue("id"),
			}
			params.Ra = parseOptionalFloat(r.FormValue("ra"))
			params.Dec = parseOptionalFloat(r.FormValue("dec"))
			params.Pixscale = parseOptionalFloat(r.FormValue("pixscale"))
			params.Radius = parseOptionalFloat(r.FormValue("radius"))
			params.WidthArcsec = parseOptionalFloat(r.FormValue("width_arcsec"))
			params.HeightArcsec = parseOptionalFloat(r.FormValue("height_arcsec"))
			params.Fieldw = parseOptionalFloat(r.FormValue("fieldw"))
			params.Fieldh = parseOptionalFloat(r.FormValue("fieldh"))
			params.Orientation = parseOptionalFloat(r.FormValue("orientation"))

			if err := DB.UpdateImage(ctx, params); err != nil {
				log.Printf("Error updating image: %v", err)
				http.Error(w, "Error updating image", http.StatusInternalServerError)
				return
			}

			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/admin")
				return
			}
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		id := r.URL.Query().Get("id")
		img, err := DB.GetImage(ctx, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		Render(w, "edit.html", img)
	})(w, r)
}

func HandleAdminNew(w http.ResponseWriter, r *http.Request) {
	adminAuth(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		if r.Method == http.MethodPost {
			r.ParseForm()
			params := database.CreateImageParams{
				ID:         r.FormValue("id"),
				Archive:    r.FormValue("archive"),
				Messier:    r.FormValue("messier"),
				Ngc:        r.FormValue("ngc"),
				Ic:         r.FormValue("ic"),
				Rcw:        r.FormValue("rcw"),
				Sh2:        r.FormValue("sh2"),
				Henize:     r.FormValue("henize"),
				Gum:        r.FormValue("gum"),
				Lbn:        r.FormValue("lbn"),
				CommonName: r.FormValue("common_name"),
				Name:       r.FormValue("name"),
				Filename:   r.FormValue("filename"),
				Thumbnail:  r.FormValue("thumbnail"),
				Type:       r.FormValue("type"),
				Camera:     r.FormValue("camera"),
				Scope:      r.FormValue("scope"),
				Mount:      r.FormValue("mount"),
				Guiding:    r.FormValue("guiding"),
				Exposure:   r.FormValue("exposure"),
				Location:   r.FormValue("location"),
				Date:       r.FormValue("date"),
				Notes:      r.FormValue("notes"),
				Blink:      r.FormValue("blink"),
				Corrector:  r.FormValue("corrector"),
				Solved:     r.FormValue("solved"),
			}
			params.Ra = parseOptionalFloat(r.FormValue("ra"))
			params.Dec = parseOptionalFloat(r.FormValue("dec"))
			params.Pixscale = parseOptionalFloat(r.FormValue("pixscale"))
			params.Radius = parseOptionalFloat(r.FormValue("radius"))
			params.WidthArcsec = parseOptionalFloat(r.FormValue("width_arcsec"))
			params.HeightArcsec = parseOptionalFloat(r.FormValue("height_arcsec"))
			params.Fieldw = parseOptionalFloat(r.FormValue("fieldw"))
			params.Fieldh = parseOptionalFloat(r.FormValue("fieldh"))
			params.Orientation = parseOptionalFloat(r.FormValue("orientation"))

			if err := DB.CreateImage(ctx, params); err != nil {
				log.Printf("Error creating image: %v", err)
				http.Error(w, "Error creating image", http.StatusInternalServerError)
				return
			}

			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/admin")
				return
			}
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		Render(w, "edit.html", database.Image{Blink: "na"})
	})(w, r)
}

func HandleAdminDelete(w http.ResponseWriter, r *http.Request) {
	adminAuth(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		ctx := context.Background()
		r.ParseForm()
		id := r.FormValue("id")

		if err := DB.DeleteImage(ctx, id); err != nil {
			log.Printf("Error deleting image %s: %v", id, err)
			http.Error(w, "Error deleting image", http.StatusInternalServerError)
			return
		}

		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})(w, r)
}

func parseOptionalFloat(s string) sql.NullFloat64 {
	if s == "" {
		return sql.NullFloat64{}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: f, Valid: true}
}
