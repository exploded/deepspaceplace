# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Deep Space Place (deepspaceplace.com) — an astrophotography portfolio and tools website. Converted from PHP/MySQL to Go/HTMX/SQLC/SQLite. Hosted on Linode (Debian), deployed via GitHub Actions on push to `main`.

## Build & Run

```bash
# Local dev (Windows) — builds exe then runs with .env loaded
build.bat

# Run existing exe (or build first if missing)
run.bat

# Linux production build (static binary, no glibc dependency)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o deepspaceplace ./cmd/server/

# Run tests
go test -v ./...

# Regenerate SQLC after editing db/queries.sql or db/schema.sql
sqlc generate
```

Requires `.env` file (copy `.env.example`). Variables: `PORT` (default 8485), `PROD`, `ADMIN_PASSWORD`.

## Architecture

Pure Go HTTP server (no framework, standard `net/http`) in `cmd/server/main.go`. Port 8485 locally, 8686 in production behind nginx.

### Routing & Handlers (`handlers/`)

- **static_pages.go** — `Render()` / `RenderPartial()` helpers, index page, static page handler, favicon/robots.txt
- **gallery.go** — `/images` and `/images/partial` (HTMX). Sorting, filtering by type/camera/scope/date, pagination (max 120/page)
- **show.go** — `/show?id=X` individual image detail with prev/next navigation, RA/Dec coordinate display
- **skymap.go** — `/skymap` interactive map + `/api/observations` JSON (plate-solved images only)
- **converter.go** — `/converter` RA/Dec decimal ↔ HMS/DMS conversion (server-side math, HTMX response)
- **moon.go** — `/moon` 60-day rise/set forecast for Melbourne using `github.com/exploded/riseset`
- **weather.go** — `/weather` + `/api/bom-satellite` proxy (fetches BoM satellite images server-side)
- **admin.go** — Cookie-based auth, CRUD for images. Protected by `adminAuth` middleware, password from `.env`
- **middleware.go** — RequestLogger, SecurityHeaders, CacheStaticAssets (7-day immutable for `/static/`)

### Templates (`templates/`)

Per-page template sets (`map[string]*template.Template`) — each page is parsed with `base.html` + its own template + any partials. This avoids Go's shared namespace issue with `{{define "content"}}`. Custom function: `safeHTML`.

- `base.html` — layout with navbar, Bootstrap
- `templates/pages/` — static content pages (equipment, observatory, etc.)
- `templates/admin/` — login, list, edit forms
- Partials: `gallery_grid.html`, `converter_result.html`

### Database

SQLite via `modernc.org/sqlite` (pure Go, no CGO). SQLC generates Go code from SQL.

- `db/schema.sql` — single `images` table (31 columns: catalog numbers, equipment, plate-solve data, metadata)
- `db/queries.sql` — all queries (list, filter, paginate, prev/next, CRUD)
- `internal/database/` — SQLC-generated code (`queries.sql.go`, `models.go`, `db.go`)
- `db/seed.sql` — initial data import
- `sqlc.yaml` — SQLC config

### Static Assets (`static/`)

CSS: `bootstrap.css`, `dsp.css`. JS: `bootstrap.bundle.js`, `htmx.min.js`. Astrophotography images in `static/` subdirectories.

### Key Dependencies

- `github.com/exploded/riseset` — moon/sun rise/set calculations. Uses pseudo-version pinned to commit. Check `AlwaysAbove`/`AlwaysBelow` before displaying `Rise`/`Set` strings.
- `modernc.org/sqlite` — pure Go SQLite driver

## Deployment

Push to `main` triggers GitHub Actions (`.github/workflows/deploy.yml`):
1. Tests run (`go test -v ./...`)
2. Builds static Linux binary (`CGO_ENABLED=0`)
3. SCPs binary + assets to Linode
4. Runs `deploy-deepspaceplace` script (stops service, replaces binary, restarts)

Deploy script at `scripts/deploy-deepspaceplace`. Server setup via `scripts/server-setup.sh`. Systemd service runs as `www-data`.

## Conventions

- `.gitattributes` enforces LF line endings for all text files (required for Linux deployment)
- HTMX is used for gallery interactions and converter — partial responses via `RenderPartial()`
- 19 static pages share a single `StaticPage()` handler factory
- The `/images/` directory (user uploads) is gitignored — served as a static file directory at runtime
