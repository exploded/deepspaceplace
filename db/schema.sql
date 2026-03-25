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
    solved        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_images_archive ON images(archive);
CREATE INDEX IF NOT EXISTS idx_images_type ON images(type);
CREATE INDEX IF NOT EXISTS idx_images_camera ON images(camera);
CREATE INDEX IF NOT EXISTS idx_images_scope ON images(scope);
CREATE INDEX IF NOT EXISTS idx_images_date ON images(date);
CREATE INDEX IF NOT EXISTS idx_images_ra_dec ON images(ra, dec);
