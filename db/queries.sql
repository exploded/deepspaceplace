-- name: GetImage :one
SELECT * FROM images WHERE id = ? LIMIT 1;

-- Gallery queries: sort by ID (default)
-- name: ListImagesByID :many
SELECT * FROM images
WHERE archive <> 'Y'
ORDER BY id
LIMIT ? OFFSET ?;

-- name: ListImagesByDateDesc :many
SELECT * FROM images
WHERE archive <> 'Y'
ORDER BY date DESC, id
LIMIT ? OFFSET ?;

-- name: ListImagesByType :many
SELECT * FROM images
WHERE archive <> 'Y'
ORDER BY type, id
LIMIT ? OFFSET ?;

-- Filtered by object type
-- name: ListImagesByIDFilterType :many
SELECT * FROM images
WHERE archive <> 'Y' AND type = ?
ORDER BY id
LIMIT ? OFFSET ?;

-- name: ListImagesByDateFilterType :many
SELECT * FROM images
WHERE archive <> 'Y' AND type = ?
ORDER BY date DESC, id
LIMIT ? OFFSET ?;

-- name: ListImagesByTypeFilterType :many
SELECT * FROM images
WHERE archive <> 'Y' AND type = ?
ORDER BY type, id
LIMIT ? OFFSET ?;

-- Filtered by camera
-- name: ListImagesByIDFilterCamera :many
SELECT * FROM images
WHERE archive <> 'Y' AND camera = ?
ORDER BY id
LIMIT ? OFFSET ?;

-- name: ListImagesByDateFilterCamera :many
SELECT * FROM images
WHERE archive <> 'Y' AND camera = ?
ORDER BY date DESC, id
LIMIT ? OFFSET ?;

-- Filtered by scope
-- name: ListImagesByIDFilterScope :many
SELECT * FROM images
WHERE archive <> 'Y' AND scope = ?
ORDER BY id
LIMIT ? OFFSET ?;

-- name: ListImagesByDateFilterScope :many
SELECT * FROM images
WHERE archive <> 'Y' AND scope = ?
ORDER BY date DESC, id
LIMIT ? OFFSET ?;

-- New images (last 12 months)
-- name: ListImagesByDateFilterNew :many
SELECT * FROM images
WHERE archive <> 'Y' AND date >= ?
ORDER BY date DESC, id
LIMIT ? OFFSET ?;

-- Count queries for pagination
-- name: CountImages :one
SELECT COUNT(*) FROM images WHERE archive <> 'Y';

-- name: CountImagesByType :one
SELECT COUNT(*) FROM images WHERE archive <> 'Y' AND type = ?;

-- name: CountImagesByCamera :one
SELECT COUNT(*) FROM images WHERE archive <> 'Y' AND camera = ?;

-- name: CountImagesByScope :one
SELECT COUNT(*) FROM images WHERE archive <> 'Y' AND scope = ?;

-- name: CountImagesNew :one
SELECT COUNT(*) FROM images WHERE archive <> 'Y' AND date >= ?;

-- Prev/Next navigation (default sort by ID)
-- name: GetPrevByID :one
SELECT id FROM images
WHERE id < ? AND archive <> 'Y'
ORDER BY id DESC
LIMIT 1;

-- name: GetNextByID :one
SELECT id FROM images
WHERE id > ? AND archive <> 'Y'
ORDER BY id ASC
LIMIT 1;

-- Prev/Next for Date sort
-- name: GetPrevByDate :one
SELECT images.id FROM images
WHERE (images.date > (SELECT i2.date FROM images i2 WHERE i2.id = ?)
       OR (images.date = (SELECT i3.date FROM images i3 WHERE i3.id = ?) AND images.id > ?))
  AND images.archive <> 'Y'
ORDER BY images.date ASC, images.id ASC
LIMIT 1;

-- name: GetNextByDate :one
SELECT images.id FROM images
WHERE (images.date < (SELECT i2.date FROM images i2 WHERE i2.id = ?)
       OR (images.date = (SELECT i3.date FROM images i3 WHERE i3.id = ?) AND images.id < ?))
  AND images.archive <> 'Y'
ORDER BY images.date DESC, images.id DESC
LIMIT 1;

-- Prev/Next for Type sort
-- name: GetPrevByType :one
SELECT images.id FROM images
WHERE (images.type < (SELECT i2.type FROM images i2 WHERE i2.id = ?)
       OR (images.type = (SELECT i3.type FROM images i3 WHERE i3.id = ?) AND images.id < ?))
  AND images.archive <> 'Y'
ORDER BY images.type DESC, images.id DESC
LIMIT 1;

-- name: GetNextByType :one
SELECT images.id FROM images
WHERE (images.type > (SELECT i2.type FROM images i2 WHERE i2.id = ?)
       OR (images.type = (SELECT i3.type FROM images i3 WHERE i3.id = ?) AND images.id > ?))
  AND images.archive <> 'Y'
ORDER BY images.type ASC, images.id ASC
LIMIT 1;

-- Skymap observations (images with plate-solve data)
-- name: ListObservations :many
SELECT id, name, thumbnail, ra, dec, fieldw, fieldh, orientation
FROM images
WHERE ra IS NOT NULL AND dec IS NOT NULL;

-- Admin: list all images
-- name: ListAllImages :many
SELECT * FROM images ORDER BY id;

-- Admin: create image
-- name: CreateImage :exec
INSERT INTO images (
    id, archive, messier, ngc, ic, rcw, sh2, henize, gum, lbn,
    common_name, name, filename, thumbnail, type, camera, scope, mount,
    guiding, exposure, location, date, notes, blink, corrector,
    ra, dec, pixscale, radius, width_arcsec, height_arcsec,
    fieldw, fieldh, orientation, solved
) VALUES (
    ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?
);

-- Admin: update image
-- name: UpdateImage :exec
UPDATE images SET
    archive = ?, messier = ?, ngc = ?, ic = ?, rcw = ?, sh2 = ?,
    henize = ?, gum = ?, lbn = ?, common_name = ?,
    name = ?, filename = ?, thumbnail = ?, type = ?, camera = ?,
    scope = ?, mount = ?, guiding = ?, exposure = ?, location = ?,
    date = ?, notes = ?, blink = ?, corrector = ?,
    ra = ?, dec = ?, pixscale = ?, radius = ?,
    width_arcsec = ?, height_arcsec = ?,
    fieldw = ?, fieldh = ?, orientation = ?, solved = ?
WHERE id = ?;

-- Admin: delete image
-- name: DeleteImage :exec
DELETE FROM images WHERE id = ?;
