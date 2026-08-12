CREATE TABLE IF NOT EXISTS images (
    id            TEXT PRIMARY KEY,
    archive       TEXT NOT NULL DEFAULT '',
    messier       TEXT NOT NULL DEFAULT '',
    ngc           TEXT NOT NULL DEFAULT '',
    ic            TEXT NOT NULL DEFAULT '',
    rcw           TEXT NOT NULL DEFAULT '',
    sh2           TEXT NOT NULL DEFAULT '',
    henize        TEXT NOT NULL DEFAULT '',
    gum           TEXT NOT NULL DEFAULT '',
    lbn           TEXT NOT NULL DEFAULT '',
    common_name   TEXT NOT NULL DEFAULT '',
    name          TEXT NOT NULL DEFAULT '',
    filename      TEXT NOT NULL DEFAULT '',
    thumbnail     TEXT NOT NULL DEFAULT '',
    type          TEXT NOT NULL DEFAULT '',
    camera        TEXT NOT NULL DEFAULT '',
    scope         TEXT NOT NULL DEFAULT '',
    mount         TEXT NOT NULL DEFAULT '',
    guiding       TEXT NOT NULL DEFAULT '',
    exposure      TEXT NOT NULL DEFAULT '',
    location      TEXT NOT NULL DEFAULT '',
    date          TEXT NOT NULL DEFAULT '',
    notes         TEXT NOT NULL DEFAULT '',
    blink         TEXT NOT NULL DEFAULT 'na',
    corrector     TEXT NOT NULL DEFAULT '',
    ra            REAL,
    dec           REAL,
    pixscale      REAL,
    radius        REAL,
    width_arcsec  REAL,
    height_arcsec REAL,
    fieldw        REAL,
    fieldh        REAL,
    orientation   REAL,
    solved        TEXT NOT NULL DEFAULT '',
    -- Whether the image is mirrored relative to the sky, as reported by the
    -- plate solver: positive for a normal image, negative for a flipped one.
    -- NULL means the row predates parity capture, in which case the annotation
    -- overlay assumes normal.
    parity        REAL,
    -- The astrometry.net submission currently being waited on, set while
    -- solved = 'p' and cleared on any terminal outcome.
    --
    -- Without it a solve that outlives the HTTP request is unrecoverable: the
    -- submission id lived only in the handler's stack frame, so when the poll
    -- loop gave up we lost the only handle on a job that was still running.
    -- lovejoy1 (submission 15814827) succeeded on nova nine minutes after we
    -- had already recorded it as failed. Persisting it means a restart or a
    -- deploy mid-solve can pick the job back up.
    --
    -- Any column added here must also be added to addMissingColumns in
    -- cmd/server/main.go. This file only runs as CREATE TABLE IF NOT EXISTS,
    -- so it does nothing to a database that already has the table -- and sqlc
    -- expands the "SELECT *" in db/queries.sql into an explicit column list at
    -- generate time, so a column that exists here and not in production makes
    -- every query fail with "no such column". Position is not significant.
    solve_subid   INTEGER
);

CREATE INDEX IF NOT EXISTS idx_images_archive ON images(archive);
CREATE INDEX IF NOT EXISTS idx_images_type ON images(type);
CREATE INDEX IF NOT EXISTS idx_images_camera ON images(camera);
CREATE INDEX IF NOT EXISTS idx_images_scope ON images(scope);
CREATE INDEX IF NOT EXISTS idx_images_date ON images(date);
CREATE INDEX IF NOT EXISTS idx_images_ra_dec ON images(ra, dec);
