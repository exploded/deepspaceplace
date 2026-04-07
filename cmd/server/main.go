package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"deepspaceplace/handlers"
	"deepspaceplace/internal/database"

	"github.com/exploded/monitor/pkg/logship"
	_ "modernc.org/sqlite"
)

func main() {
	loadEnvFile(".env")

	// Set up log shipping to monitor portal
	var ship *logship.Handler
	monitorURL := os.Getenv("MONITOR_URL")
	monitorKey := os.Getenv("MONITOR_API_KEY")

	if monitorURL != "" && monitorKey != "" {
		ship = logship.New(logship.Options{
			Endpoint: monitorURL + "/api/logs",
			APIKey:   monitorKey,
			App:      "deepspaceplace",
			Level:    slog.LevelWarn,
		})

		logger := slog.New(logship.Multi(
			slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}),
			ship,
		))
		slog.SetDefault(logger)
		slog.Warn("deepspaceplace app started, log shipping active", "endpoint", monitorURL+"/api/logs")
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8485"
	}
	httpPort = ":" + httpPort

	prod := os.Getenv("PROD") == "True"
	slog.Info("Config", "prod", prod, "port", httpPort)

	// Open SQLite database
	db, err := sql.Open("sqlite", "deepspaceplace.db")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create tables if they don't exist
	schemaSQL, err := os.ReadFile("db/schema.sql")
	if err != nil {
		log.Fatalf("Failed to read schema: %v", err)
	}
	if _, err := db.Exec(string(schemaSQL)); err != nil {
		log.Fatalf("Failed to create tables: %v", err)
	}

	queries := database.New(db)
	handlers.DB = queries
	handlers.Prod = prod

	// Parse templates
	templates, err := parseTemplates()
	if err != nil {
		log.Fatalf("Error parsing templates: %v", err)
	}
	handlers.Templates = templates

	// Set up routes
	mux := http.NewServeMux()

	// Home
	mux.HandleFunc("/", handlers.HandleIndex)

	// Gallery (database-driven)
	mux.HandleFunc("/images", handlers.HandleGallery)
	mux.HandleFunc("/images/partial", handlers.HandleGalleryPartial)
	mux.HandleFunc("/show", handlers.HandleShow)

	// Skymap
	mux.HandleFunc("/skymap", handlers.HandleSkymap)
	mux.HandleFunc("/api/observations", handlers.HandleObservationsJSON)

	// Coordinate converter
	mux.HandleFunc("/converter", handlers.HandleConverter)

	// Moon rise/set
	mux.HandleFunc("/moon", handlers.HandleMoon)

	// Weather
	mux.HandleFunc("/weather", handlers.HandleWeather)
	mux.HandleFunc("/api/bom-satellite", handlers.HandleBOMProxy)

	// Admin (login/logout are public, everything else requires auth)
	mux.HandleFunc("/admin/login", handlers.HandleAdminLogin)
	mux.HandleFunc("/admin/logout", handlers.HandleAdminLogout)
	mux.HandleFunc("/admin", handlers.AdminAuth(handlers.HandleAdmin))
	mux.HandleFunc("/admin/edit", handlers.AdminAuth(handlers.HandleAdminEdit))
	mux.HandleFunc("/admin/new", handlers.AdminAuth(handlers.HandleAdminNew))
	mux.HandleFunc("/admin/delete", handlers.AdminAuth(handlers.HandleAdminDelete))
	mux.HandleFunc("/admin/resize", handlers.AdminAuth(handlers.HandleAdminResize))
	mux.HandleFunc("/admin/platesolve", handlers.AdminAuth(handlers.HandleAdminPlateSolve))

	// Static content pages
	mux.HandleFunc("/equipment", handlers.StaticPage("equipment.html", "/equipment",
		"Equipment", "Astrophotography telescope and camera equipment summary with field of view and resolution comparisons."))
	mux.HandleFunc("/observatory", handlers.StaticPage("observatory.html", "/observatory",
		"Observatory", "Home observatory build with roll-off roof design for astrophotography."))
	mux.HandleFunc("/links", handlers.StaticPage("links.html", "/links",
		"Links", "Useful astrophotography links including software, gear, and Australian astrophotography resources."))
	mux.HandleFunc("/timelapse", handlers.StaticPage("timelapse.html", "/timelapse",
		"Timelapse Videos", "Timelapse and astrophotography videos."))
	mux.HandleFunc("/terrestrial", handlers.StaticPage("terrestrial.html", "/terrestrial",
		"Terrestrial Images", "Terrestrial landscape photography from Australia and Europe."))
	mux.HandleFunc("/8se", handlers.StaticPage("8se.html", "/8se",
		"Celestron NexStar 8 SE", "Celestron NexStar 8 SE telescope setup and review for astrophotography."))
	mux.HandleFunc("/at8in", handlers.StaticPage("at8in.html", "/at8in",
		"Astro-Tech 8\" f/4 Newtonian", "Astro-Tech 8\" f/4 Imaging Newtonian telescope setup, collimation, and focusing tips."))
	mux.HandleFunc("/at12in", handlers.StaticPage("at12in.html", "/at12in",
		"12\" f/4 Newtonian", "12\" f/4 Newtonian reflector telescope build and upgrades for deep space imaging."))
	mux.HandleFunc("/ed127", handlers.StaticPage("ed127.html", "/ed127",
		"ED127 APO", "ED127 APO refractor review with chromatic aberration tests and image quality analysis."))
	mux.HandleFunc("/gso8rc", handlers.StaticPage("gso8rc.html", "/gso8rc",
		"GSO 8\" RC", "GSO 8\" Ritchey-Chretien telescope setup and guides for astrophotography."))
	mux.HandleFunc("/mn152", handlers.StaticPage("mn152.html", "/mn152",
		"MN152 Maksutov-Newtonian", "Maxvision MN152 F4.8 Maksutov-Newtonian telescope review and specifications."))
	mux.HandleFunc("/meteor", handlers.StaticPage("meteor.html", "/meteor",
		"Meteor Camera", "UFOCapture meteor detection camera setup and gallery of captured meteors."))
	mux.HandleFunc("/lightpollution", handlers.StaticPage("lightpollution.html", "/lightpollution",
		"Light Pollution", "Light pollution information and resources for astrophotography site selection."))
	mux.HandleFunc("/gso8rcpointing", handlers.StaticPage("gso8rcpointing.html", "/gso8rcpointing",
		"GSO 8\" RC Pointing Accuracy", "Pointing accuracy tests and results for the GSO 8\" Ritchey-Chretien telescope."))
	mux.HandleFunc("/gso8rccollimate", handlers.StaticPage("gso8rccollimate.html", "/gso8rccollimate",
		"GSO 8\" RC Collimation", "Step-by-step collimation guide for the GSO 8\" Ritchey-Chretien telescope."))
	mux.HandleFunc("/abbreviations", handlers.StaticPage("abbreviations.html", "/abbreviations",
		"Astrophotography Terminology", "Glossary of astrophotography terms, abbreviations, and definitions."))
	mux.HandleFunc("/eq6", handlers.StaticPage("eq6.html", "/eq6",
		"EQ6 Tuneup", "Skywatcher EQ6 Pro mount tuneup and maintenance guide for astrophotography."))
	mux.HandleFunc("/bahtinovmask", handlers.StaticPage("bahtinovmask.html", "/bahtinovmask",
		"Bahtinov Mask", "Bahtinov mask focusing aid for precise telescope focus in astrophotography."))
	mux.HandleFunc("/maximdltips", handlers.StaticPage("maximdltips.html", "/maximdltips",
		"MaximDL Tips", "Tips and techniques for using MaxIm DL imaging software in astrophotography."))
	mux.HandleFunc("/thermalcamera", handlers.StaticPage("thermalcamera.html", "/thermalcamera",
		"Thermal Camera", "Thermal camera images of telescope and observatory equipment."))
	mux.HandleFunc("/currentsetup", handlers.StaticPage("currentsetup.html", "/currentsetup",
		"Current Setup", "Current astrophotography equipment setup: Paramount ME mount, ASI2600MM Duo camera, Baader filters, and 12 inch f/4 Newtonian telescope."))

	// Legacy .php redirects (301) — old site used .php extensions
	phpPages := []string{
		"index", "images", "show", "equipment", "observatory", "links",
		"timelapse", "terrestrial", "8se", "at8in", "at12in", "ed127",
		"gso8rc", "mn152", "meteor", "lightpollution", "gso8rcpointing",
		"gso8rccollimate", "abbreviations", "eq6", "bahtinovmask",
		"maximdltips", "thermalcamera", "moon", "weather", "skymap",
		"coordinate-converter", "bom_satellite_proxy",
	}
	for _, name := range phpPages {
		mux.HandleFunc("/"+name+".php", handlers.HandlePHPRedirect)
	}

	// Favicon, robots.txt, sitemap, webmanifest
	mux.HandleFunc("/favicon.ico", handlers.HandleFavicon)
	mux.HandleFunc("/robots.txt", handlers.HandleRobotsTxt)
	mux.HandleFunc("/site.webmanifest", handlers.HandleWebManifest)
	mux.HandleFunc("/sitemap.xml", handlers.HandleSitemap)

	// Static file servers - serve from original paths to preserve URLs
	path, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get working directory: %v", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", handlers.CacheStaticAssets(http.FileServer(http.Dir(filepath.Join(path, "static"))))))
	mux.Handle("/images/", handlers.CacheStaticAssets(http.FileServer(http.Dir(path))))
	mux.Handle("/files/", handlers.CacheStaticAssets(http.FileServer(http.Dir(path))))
	mux.Handle("/meteor/", http.StripPrefix("/meteor/", handlers.CacheStaticAssets(http.FileServer(http.Dir(filepath.Join(path, "static", "meteor"))))))
	mux.Handle("/data/", handlers.CacheStaticAssets(http.FileServer(http.Dir(path))))

	// Build server with middleware
	srv := &http.Server{
		Addr:         httpPort,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
		Handler:      handlers.RequestLogger(handlers.WWWRedirect(handlers.SecurityHeaders(mux))),
	}

	// Start server
	go func() {
		slog.Info("Starting HTTP server", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe() failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Shutting down server")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}
	if ship != nil {
		ship.Shutdown()
	}
	slog.Info("Server exited")
}

func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			os.Setenv(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
}

func parseTemplates() (map[string]*template.Template, error) {
	templates := make(map[string]*template.Template)

	// Collect all page template files
	patterns := []string{
		"templates/*.html",
		"templates/pages/*.html",
		"templates/admin/*.html",
	}

	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, match := range matches {
			normalized := filepath.ToSlash(match)
			if normalized == "templates/base.html" {
				continue
			}

			// Each page gets its own template set: base + page
			name := filepath.Base(match)
			t, err := template.New("").Funcs(handlers.TemplateFuncs).ParseFiles("templates/base.html", match)
			if err != nil {
				return nil, fmt.Errorf("parsing %s: %w", match, err)
			}

			// Also parse partials that pages might reference
			partialPatterns := []string{"templates/gallery_grid.html", "templates/converter_result.html"}
			for _, p := range partialPatterns {
				if _, statErr := os.Stat(p); statErr == nil {
					_, err = t.ParseFiles(p)
					if err != nil {
						return nil, fmt.Errorf("parsing partial %s for %s: %w", p, match, err)
					}
				}
			}

			templates[name] = t
			slog.Info("Loaded template", "name", name)
		}
	}

	return templates, nil
}
