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
	} else {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	httpPort := os.Getenv("PORT")
	if httpPort == "" {
		httpPort = "8485"
	}
	httpPort = ":" + httpPort

	prod := os.Getenv("PROD") == "True"
	log.Printf("Production: %v", prod)
	log.Printf("HTTP Port: %s", httpPort)

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
	mux.HandleFunc("/equipment", handlers.StaticPage("equipment.html"))
	mux.HandleFunc("/observatory", handlers.StaticPage("observatory.html"))
	mux.HandleFunc("/links", handlers.StaticPage("links.html"))
	mux.HandleFunc("/timelapse", handlers.StaticPage("timelapse.html"))
	mux.HandleFunc("/terrestrial", handlers.StaticPage("terrestrial.html"))
	mux.HandleFunc("/8se", handlers.StaticPage("8se.html"))
	mux.HandleFunc("/at8in", handlers.StaticPage("at8in.html"))
	mux.HandleFunc("/at12in", handlers.StaticPage("at12in.html"))
	mux.HandleFunc("/ed127", handlers.StaticPage("ed127.html"))
	mux.HandleFunc("/gso8rc", handlers.StaticPage("gso8rc.html"))
	mux.HandleFunc("/mn152", handlers.StaticPage("mn152.html"))
	mux.HandleFunc("/meteor", handlers.StaticPage("meteor.html"))
	mux.HandleFunc("/lightpollution", handlers.StaticPage("lightpollution.html"))
	mux.HandleFunc("/gso8rcpointing", handlers.StaticPage("gso8rcpointing.html"))
	mux.HandleFunc("/gso8rccollimate", handlers.StaticPage("gso8rccollimate.html"))
	mux.HandleFunc("/abbreviations", handlers.StaticPage("abbreviations.html"))
	mux.HandleFunc("/eq6", handlers.StaticPage("eq6.html"))
	mux.HandleFunc("/bahtinovmask", handlers.StaticPage("bahtinovmask.html"))
	mux.HandleFunc("/maximdltips", handlers.StaticPage("maximdltips.html"))
	mux.HandleFunc("/thermalcamera", handlers.StaticPage("thermalcamera.html"))

	// Favicon and robots.txt
	mux.HandleFunc("/favicon.ico", handlers.HandleFavicon)
	mux.HandleFunc("/robots.txt", handlers.HandleRobotsTxt)

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
		Handler:      handlers.RequestLogger(handlers.SecurityHeaders(mux)),
	}

	// Start server
	go func() {
		log.Printf("Starting HTTP server on %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe() failed: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v", err)
	}
	if ship != nil {
		ship.Shutdown()
	}
	log.Println("Server exited")
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
			t, err := template.New("").ParseFiles("templates/base.html", match)
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
			log.Printf("Loaded template: %s", name)
		}
	}

	return templates, nil
}
