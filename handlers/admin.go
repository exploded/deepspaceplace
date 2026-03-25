package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"deepspaceplace/internal/database"
)

var (
	adminSessionToken  string
	adminSessionExpiry time.Time
	adminCSRFToken     string
	adminSessionMu     sync.Mutex

	loginAttempts    int
	loginLockedUntil time.Time
	loginMu          sync.Mutex
)

const (
	maxLoginAttempts = 5
	loginLockoutDur  = 15 * time.Minute
)

// AdminAuth is middleware that checks for a valid admin session.
// Apply at the route level in main.go.
func AdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err == nil && cookie.Value != "" {
			adminSessionMu.Lock()
			valid := adminSessionToken != "" &&
				subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(adminSessionToken)) == 1 &&
				time.Now().Before(adminSessionExpiry)
			adminSessionMu.Unlock()
			if valid {
				next(w, r)
				return
			}
		}
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
	}
}

// getCSRFToken returns the current CSRF token (caller must hold adminSessionMu or accept a race).
func getCSRFToken() string {
	adminSessionMu.Lock()
	defer adminSessionMu.Unlock()
	return adminCSRFToken
}

// validateCSRF checks the csrf_token form field against the session CSRF token.
func validateCSRF(r *http.Request) bool {
	token := r.FormValue("csrf_token")
	if token == "" {
		// Also check HTMX header for AJAX requests
		token = r.Header.Get("X-CSRF-Token")
	}
	expected := getCSRFToken()
	if token == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

func HandleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		r.ParseForm()

		// Rate limiting
		loginMu.Lock()
		if !loginLockedUntil.IsZero() && time.Now().Before(loginLockedUntil) {
			remaining := time.Until(loginLockedUntil).Round(time.Second)
			loginMu.Unlock()
			log.Printf("Login locked out, %s remaining", remaining)
			Render(w, "login.html", "Too many failed attempts. Try again later.")
			return
		}
		loginMu.Unlock()

		password := r.FormValue("password")

		adminPass := os.Getenv("ADMIN_PASSWORD")
		if adminPass == "" {
			log.Println("WARNING: ADMIN_PASSWORD not set, admin login disabled")
			Render(w, "login.html", "Admin login disabled (no password configured)")
			return
		}

		if subtle.ConstantTimeCompare([]byte(password), []byte(adminPass)) == 1 {
			// Success — reset attempts
			loginMu.Lock()
			loginAttempts = 0
			loginLockedUntil = time.Time{}
			loginMu.Unlock()

			token := generateToken()
			csrf := generateToken()
			adminSessionMu.Lock()
			adminSessionToken = token
			adminCSRFToken = csrf
			adminSessionExpiry = time.Now().Add(24 * time.Hour)
			adminSessionMu.Unlock()

			http.SetCookie(w, &http.Cookie{
				Name:     "admin_session",
				Value:    token,
				Path:     "/admin",
				HttpOnly: true,
				Secure:   Prod,
				SameSite: http.SameSiteStrictMode,
			})
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		// Failed — increment attempts
		loginMu.Lock()
		loginAttempts++
		if loginAttempts >= maxLoginAttempts {
			loginLockedUntil = time.Now().Add(loginLockoutDur)
			log.Printf("Login locked out for %s after %d failed attempts", loginLockoutDur, loginAttempts)
			loginAttempts = 0
		}
		loginMu.Unlock()

		Render(w, "login.html", "Invalid password")
		return
	}

	Render(w, "login.html", nil)
}

func HandleAdminLogout(w http.ResponseWriter, r *http.Request) {
	adminSessionMu.Lock()
	adminSessionToken = ""
	adminCSRFToken = ""
	adminSessionExpiry = time.Time{}
	adminSessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "admin_session",
		Value:    "",
		Path:     "/admin",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func HandleAdmin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	images, err := DB.ListAllImages(ctx)
	if err != nil {
		log.Printf("Error listing images: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	type adminListData struct {
		Images    []database.Image
		CSRFToken string
	}
	Render(w, "list.html", adminListData{
		Images:    images,
		CSRFToken: getCSRFToken(),
	})
}

func HandleAdminEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodPost {
		r.ParseForm()
		if !validateCSRF(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		f := parseImageForm(r)
		params := database.UpdateImageParams{
			Archive: f.Archive, Messier: f.Messier, Ngc: f.Ngc, Ic: f.Ic,
			Rcw: f.Rcw, Sh2: f.Sh2, Henize: f.Henize, Gum: f.Gum, Lbn: f.Lbn,
			CommonName: f.CommonName, Name: f.Name, Filename: f.Filename,
			Thumbnail: f.Thumbnail, Type: f.Type, Camera: f.Camera,
			Scope: f.Scope, Mount: f.Mount, Guiding: f.Guiding,
			Exposure: f.Exposure, Location: f.Location, Date: f.Date,
			Notes: f.Notes, Blink: f.Blink, Corrector: f.Corrector,
			Solved: f.Solved, ID: r.FormValue("id"),
			Ra: f.Ra, Dec: f.Dec, Pixscale: f.Pixscale, Radius: f.Radius,
			WidthArcsec: f.WidthArcsec, HeightArcsec: f.HeightArcsec,
			Fieldw: f.Fieldw, Fieldh: f.Fieldh, Orientation: f.Orientation,
		}

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

	type editData struct {
		database.Image
		CSRFToken string
	}
	Render(w, "edit.html", editData{Image: img, CSRFToken: getCSRFToken()})
}

func HandleAdminNew(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if r.Method == http.MethodPost {
		r.ParseForm()
		if !validateCSRF(r) {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}

		f := parseImageForm(r)
		params := database.CreateImageParams{
			ID: r.FormValue("id"),
			Archive: f.Archive, Messier: f.Messier, Ngc: f.Ngc, Ic: f.Ic,
			Rcw: f.Rcw, Sh2: f.Sh2, Henize: f.Henize, Gum: f.Gum, Lbn: f.Lbn,
			CommonName: f.CommonName, Name: f.Name, Filename: f.Filename,
			Thumbnail: f.Thumbnail, Type: f.Type, Camera: f.Camera,
			Scope: f.Scope, Mount: f.Mount, Guiding: f.Guiding,
			Exposure: f.Exposure, Location: f.Location, Date: f.Date,
			Notes: f.Notes, Blink: f.Blink, Corrector: f.Corrector,
			Solved: f.Solved,
			Ra: f.Ra, Dec: f.Dec, Pixscale: f.Pixscale, Radius: f.Radius,
			WidthArcsec: f.WidthArcsec, HeightArcsec: f.HeightArcsec,
			Fieldw: f.Fieldw, Fieldh: f.Fieldh, Orientation: f.Orientation,
		}

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

	type editData struct {
		database.Image
		CSRFToken string
	}
	Render(w, "edit.html", editData{Image: database.Image{Blink: "na"}, CSRFToken: getCSRFToken()})
}

func HandleAdminDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.ParseForm()
	if !validateCSRF(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	ctx := r.Context()
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
}

type imageFormFields struct {
	Archive, Messier, Ngc, Ic, Rcw, Sh2, Henize, Gum, Lbn string
	CommonName, Name, Filename, Thumbnail                  string
	Type, Camera, Scope, Mount, Guiding                    string
	Exposure, Location, Date, Notes                        string
	Blink, Corrector, Solved                               string
	Ra, Dec, Pixscale, Radius                              sql.NullFloat64
	WidthArcsec, HeightArcsec                              sql.NullFloat64
	Fieldw, Fieldh, Orientation                            sql.NullFloat64
}

func parseImageForm(r *http.Request) imageFormFields {
	return imageFormFields{
		Archive:      r.FormValue("archive"),
		Messier:      r.FormValue("messier"),
		Ngc:          r.FormValue("ngc"),
		Ic:           r.FormValue("ic"),
		Rcw:          r.FormValue("rcw"),
		Sh2:          r.FormValue("sh2"),
		Henize:       r.FormValue("henize"),
		Gum:          r.FormValue("gum"),
		Lbn:          r.FormValue("lbn"),
		CommonName:   r.FormValue("common_name"),
		Name:         r.FormValue("name"),
		Filename:     r.FormValue("filename"),
		Thumbnail:    r.FormValue("thumbnail"),
		Type:         r.FormValue("type"),
		Camera:       r.FormValue("camera"),
		Scope:        r.FormValue("scope"),
		Mount:        r.FormValue("mount"),
		Guiding:      r.FormValue("guiding"),
		Exposure:     r.FormValue("exposure"),
		Location:     r.FormValue("location"),
		Date:         r.FormValue("date"),
		Notes:        r.FormValue("notes"),
		Blink:        r.FormValue("blink"),
		Corrector:    r.FormValue("corrector"),
		Solved:       r.FormValue("solved"),
		Ra:           parseOptionalFloat(r.FormValue("ra")),
		Dec:          parseOptionalFloat(r.FormValue("dec")),
		Pixscale:     parseOptionalFloat(r.FormValue("pixscale")),
		Radius:       parseOptionalFloat(r.FormValue("radius")),
		WidthArcsec:  parseOptionalFloat(r.FormValue("width_arcsec")),
		HeightArcsec: parseOptionalFloat(r.FormValue("height_arcsec")),
		Fieldw:       parseOptionalFloat(r.FormValue("fieldw")),
		Fieldh:       parseOptionalFloat(r.FormValue("fieldh")),
		Orientation:  parseOptionalFloat(r.FormValue("orientation")),
	}
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
