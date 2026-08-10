// Package wcs reconstructs a gnomonic (TAN) world coordinate system from the
// summary plate-solve values Astrometry.net returns, and projects sky
// coordinates onto image pixels.
//
// This is deliberately a summary WCS, not a full one: we have the field centre,
// the pixel scale, a single rotation angle and a parity flag, but no CD matrix
// and no SIP distortion terms. That is accurate to well under a pixel across a
// typical amateur field, which is far more than enough to place annotation
// labels, but it is not good enough for astrometry.
package wcs

import "math"

// arcsecPerRadian converts the radian-valued standard coordinates produced by
// the gnomonic projection into the arcseconds that pixscale is expressed in.
const arcsecPerRadian = 206264.806247096355

// Parity records whether the image is mirrored relative to the sky.
type Parity int

const (
	// Normal is an unmirrored image: with north up, east is to the left.
	Normal Parity = -1
	// Mirrored is a flipped image: with north up, east is to the right.
	// Any optical train with an odd number of reflections produces these —
	// a refractor with a star diagonal, for instance.
	Mirrored Parity = 1
)

// WCS is a tangent-plane projection centred on (RA0, Dec0).
type WCS struct {
	RA0, Dec0   float64 // field centre, degrees
	PixScale    float64 // arcsec per pixel
	Orientation float64 // position angle of the image's "up" vector, degrees east of north
	Parity      Parity
	W, H        int // image dimensions in pixels
}

// New builds a WCS and reports whether the inputs describe a usable one.
// Callers get a bool rather than an error because every rejection here means
// the same thing: this image has no overlay.
func New(ra, dec, pixScale, orientation float64, parity Parity, w, h int) (WCS, bool) {
	if pixScale <= 0 || w <= 0 || h <= 0 {
		return WCS{}, false
	}
	if math.IsNaN(ra) || math.IsNaN(dec) || dec < -90 || dec > 90 {
		return WCS{}, false
	}
	if parity != Normal && parity != Mirrored {
		parity = Normal
	}
	return WCS{
		RA0:         ra,
		Dec0:        dec,
		PixScale:    pixScale,
		Orientation: orientation,
		Parity:      parity,
		W:           w,
		H:           h,
	}, true
}

// FromAstrometry builds a WCS from the summary values Astrometry.net's
// calibration endpoint returns, converting its conventions to this package's.
//
// Two conversions happen here, and both matter:
//
// Orientation is negated. Astrometry.net works in FITS pixel coordinates,
// whose y axis points up, while an image's rows run downward. Going between
// the two is a reflection, which reverses the sense of the rotation. This was
// established empirically against three solved images on the site — the
// Horsehead, LDN 1634 and M16, with stored orientations of 89.6, 182.3 and
// 124.7 degrees — by reading north and east off the coordinate grids of the
// annotated JPEGs astrometry.net produced for them. TestAgainstSolvedImages
// pins all six of those directions.
//
// Parity is likewise flipped by that same reflection, so a positive parity
// from Astrometry.net means an unmirrored image here. Unlike the orientation
// convention, this half could NOT be verified: all three reference images are
// unmirrored, and no solved image on the site is known to be flipped. If an
// image's annotations ever come out mirrored, this is the first place to look,
// and admins can override it per image.
func FromAstrometry(ra, dec, pixScale, orientation, parityRaw float64, w, h int) (WCS, bool) {
	parity := Mirrored
	if parityRaw >= 0 {
		parity = Normal
	}
	return New(ra, dec, pixScale, -orientation, parity, w, h)
}

// PixelDims derives image dimensions in pixels from the angular field size and
// the pixel scale, since the solve response records the field in arcseconds but
// never the pixel grid itself. It reports false when the inputs cannot describe
// a real image.
func PixelDims(widthArcsec, heightArcsec, pixScale float64) (w, h int, ok bool) {
	if pixScale <= 0 || widthArcsec <= 0 || heightArcsec <= 0 {
		return 0, 0, false
	}
	w = int(math.Round(widthArcsec / pixScale))
	h = int(math.Round(heightArcsec / pixScale))
	// A plausibility band. Below this the solve is nonsense; above it we would
	// be generating an overlay for an image nobody has.
	if w < 16 || h < 16 || w > 100000 || h > 100000 {
		return 0, 0, false
	}
	return w, h, true
}

// standard converts a sky position to gnomonic standard coordinates in
// arcseconds relative to the field centre, with xi east-positive and eta
// north-positive. It reports false for points on the far side of the sky from
// the tangent point, which have no projection.
func (w WCS) standard(ra, dec float64) (xi, eta float64, ok bool) {
	var (
		ra0  = w.RA0 * math.Pi / 180
		dec0 = w.Dec0 * math.Pi / 180
		a    = ra * math.Pi / 180
		d    = dec * math.Pi / 180
	)
	dRA := a - ra0
	sinD, cosD := math.Sincos(d)
	sinD0, cosD0 := math.Sincos(dec0)
	sinDRA, cosDRA := math.Sincos(dRA)

	cosc := sinD0*sinD + cosD0*cosD*cosDRA
	if cosc <= 1e-12 {
		return 0, 0, false
	}
	xi = cosD * sinDRA / cosc * arcsecPerRadian
	eta = (cosD0*sinD - sinD0*cosD*cosDRA) / cosc * arcsecPerRadian
	return xi, eta, true
}

// Project maps a sky position to image pixel coordinates, with x increasing to
// the right and y increasing downward from the top-left corner. It reports
// false when the position does not project onto the tangent plane at all.
//
// Points outside the frame still project successfully — the caller decides
// what margin to allow, since an object's glyph can legitimately overhang the
// edge.
func (w WCS) Project(ra, dec float64) (x, y float64, ok bool) {
	xi, eta, ok := w.standard(ra, dec)
	if !ok {
		return 0, 0, false
	}
	sinT, cosT := math.Sincos(w.Orientation * math.Pi / 180)
	p := float64(w.Parity)

	// The image "up" vector sits at position angle Orientation east of north;
	// "right" is a quarter turn from it, in the direction parity dictates.
	// y is negated because image rows run downward while "up" runs up.
	dx := p * (xi*cosT - eta*sinT) / w.PixScale
	dy := -(xi*sinT + eta*cosT) / w.PixScale

	return float64(w.W)/2 + dx, float64(w.H)/2 + dy, true
}

// Deproject is the inverse of Project: it maps image pixel coordinates back to
// a sky position in degrees. Used to work out which slice of sky the frame
// covers when laying out a coordinate grid.
func (w WCS) Deproject(x, y float64) (ra, dec float64) {
	sinT, cosT := math.Sincos(w.Orientation * math.Pi / 180)
	p := float64(w.Parity)

	up := -(y - float64(w.H)/2) * w.PixScale
	right := p * (x - float64(w.W)/2) * w.PixScale

	xi := (up*sinT + right*cosT) / arcsecPerRadian
	eta := (up*cosT - right*sinT) / arcsecPerRadian

	dec0 := w.Dec0 * math.Pi / 180
	sinD0, cosD0 := math.Sincos(dec0)

	rho := math.Hypot(xi, eta)
	if rho < 1e-15 {
		return normalizeRA(w.RA0), w.Dec0
	}
	c := math.Atan(rho)
	sinC, cosC := math.Sincos(c)

	dec = math.Asin(cosC*sinD0 + eta*sinC*cosD0/rho)
	ra = w.RA0*math.Pi/180 + math.Atan2(xi*sinC, rho*cosD0*cosC-eta*sinD0*sinC)

	return normalizeRA(ra * 180 / math.Pi), dec * 180 / math.Pi
}

// Contains reports whether a pixel position falls within the frame, expanded by
// margin pixels on every side.
func (w WCS) Contains(x, y, margin float64) bool {
	return x >= -margin && y >= -margin &&
		x <= float64(w.W)+margin && y <= float64(w.H)+margin
}

// RadiusDeg is the angular radius of the circle circumscribing the frame — the
// search radius that is guaranteed to cover every corner.
func (w WCS) RadiusDeg() float64 {
	wDeg := float64(w.W) * w.PixScale / 3600
	hDeg := float64(w.H) * w.PixScale / 3600
	return math.Hypot(wDeg, hDeg) / 2
}

// Separation returns the angular distance in degrees between two sky positions.
func Separation(ra1, dec1, ra2, dec2 float64) float64 {
	var (
		d1 = dec1 * math.Pi / 180
		d2 = dec2 * math.Pi / 180
		dr = (ra2 - ra1) * math.Pi / 180
	)
	sinD1, cosD1 := math.Sincos(d1)
	sinD2, cosD2 := math.Sincos(d2)
	sinDR, cosDR := math.Sincos(dr)

	// Vincenty form — stable at both small and large separations, unlike the
	// plain spherical law of cosines.
	num := math.Hypot(cosD2*sinDR, cosD1*sinD2-sinD1*cosD2*cosDR)
	den := sinD1*sinD2 + cosD1*cosD2*cosDR
	return math.Atan2(num, den) * 180 / math.Pi
}

func normalizeRA(ra float64) float64 {
	ra = math.Mod(ra, 360)
	if ra < 0 {
		ra += 360
	}
	return ra
}
