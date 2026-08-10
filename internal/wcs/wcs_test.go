package wcs

import (
	"math"
	"testing"
)

// A convenient test frame: 1 arcsec/pixel makes pixel offsets and arcsecond
// offsets the same number, so the expected values below are readable.
func testWCS(orientation float64, p Parity) WCS {
	w, ok := New(80.0, -69.0, 1.0, orientation, p, 1000, 800)
	if !ok {
		panic("test WCS rejected")
	}
	return w
}

func closeTo(t *testing.T, got, want, tol float64, what string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.4f, want %.4f (tol %g)", what, got, want, tol)
	}
}

// offsetSky returns the sky position a given angular distance from a starting
// point along a given position angle (degrees east of north). Stepping east by
// "RA + d/cos(dec)" is only a small-angle approximation and drifts by a
// measurable fraction of a pixel at high declination, which would make the
// cardinal-direction expectations below wrong rather than the code.
func offsetSky(ra0, dec0, distArcsec, paDeg float64) (ra, dec float64) {
	d := distArcsec / 3600 * math.Pi / 180
	pa := paDeg * math.Pi / 180
	d0 := dec0 * math.Pi / 180

	sinD0, cosD0 := math.Sincos(d0)
	sinD, cosD := math.Sincos(d)
	sinPA, cosPA := math.Sincos(pa)

	decR := math.Asin(sinD0*cosD + cosD0*sinD*cosPA)
	raR := ra0*math.Pi/180 + math.Atan2(sinPA*sinD*cosD0, cosD-sinD0*math.Sin(decR))
	return raR * 180 / math.Pi, decR * 180 / math.Pi
}

func TestProjectCentre(t *testing.T) {
	for _, p := range []Parity{Normal, Mirrored} {
		for _, o := range []float64{0, 37, 90, 180, 275} {
			w := testWCS(o, p)
			x, y, ok := w.Project(w.RA0, w.Dec0)
			if !ok {
				t.Fatalf("centre failed to project (parity %d, orientation %g)", p, o)
			}
			closeTo(t, x, 500, 1e-6, "centre x")
			closeTo(t, y, 400, 1e-6, "centre y")
		}
	}
}

// TestProjectCardinals pins the sign conventions: which way is north, which way
// is east, and what parity actually flips. Everything downstream — glyph
// placement, the compass rose, the grid — inherits these signs, so they are the
// single most important thing in this package to get right.
func TestProjectCardinals(t *testing.T) {
	const off = 60 // arcsec

	cases := []struct {
		name        string
		orientation float64
		parity      Parity
		// expected pixel offset from centre for a point `off` arcsec north,
		// then for a point `off` arcsec east
		northDX, northDY float64
		eastDX, eastDY   float64
	}{
		{
			name: "unmirrored, north up", orientation: 0, parity: Normal,
			northDX: 0, northDY: -off, // north is up
			eastDX: -off, eastDY: 0, // east is left
		},
		{
			name: "mirrored, north up", orientation: 0, parity: Mirrored,
			northDX: 0, northDY: -off, // north still up
			eastDX: off, eastDY: 0, // east flips to the right
		},
		{
			name: "unmirrored, rotated 90", orientation: 90, parity: Normal,
			northDX: off, northDY: 0, // north swings to the right
			eastDX: 0, eastDY: -off, // east swings up
		},
		{
			name: "unmirrored, rotated 180", orientation: 180, parity: Normal,
			northDX: 0, northDY: off, // upside down
			eastDX: off, eastDY: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := testWCS(tc.orientation, tc.parity)

			// due north of the centre: position angle 0
			nRA, nDec := offsetSky(w.RA0, w.Dec0, off, 0)
			nx, ny, ok := w.Project(nRA, nDec)
			if !ok {
				t.Fatal("north point failed to project")
			}
			closeTo(t, nx-500, tc.northDX, 0.01, "north dx")
			closeTo(t, ny-400, tc.northDY, 0.01, "north dy")

			// due east of the centre: position angle 90
			eRA, eDec := offsetSky(w.RA0, w.Dec0, off, 90)
			ex, ey, ok := w.Project(eRA, eDec)
			if !ok {
				t.Fatal("east point failed to project")
			}
			closeTo(t, ex-500, tc.eastDX, 0.01, "east dx")
			closeTo(t, ey-400, tc.eastDY, 0.01, "east dy")
		})
	}
}

func TestProjectDeprojectRoundTrip(t *testing.T) {
	pixels := [][2]float64{{0, 0}, {1000, 800}, {500, 400}, {123, 45}, {999, 1}}

	for _, p := range []Parity{Normal, Mirrored} {
		for _, o := range []float64{0, 33.5, 90, 212, 359} {
			w := testWCS(o, p)
			for _, px := range pixels {
				ra, dec := w.Deproject(px[0], px[1])
				gx, gy, ok := w.Project(ra, dec)
				if !ok {
					t.Fatalf("round trip failed to project back (parity %d, orientation %g)", p, o)
				}
				closeTo(t, gx, px[0], 1e-6, "round trip x")
				closeTo(t, gy, px[1], 1e-6, "round trip y")
			}
		}
	}
}

// A pole-adjacent centre is where a naive projection falls apart, so check the
// round trip survives it.
func TestRoundTripNearPole(t *testing.T) {
	w, ok := New(45, -89.2, 2.0, 17, Normal, 600, 600)
	if !ok {
		t.Fatal("New rejected a valid near-pole WCS")
	}
	for _, px := range [][2]float64{{0, 0}, {600, 600}, {310, 290}} {
		ra, dec := w.Deproject(px[0], px[1])
		gx, gy, ok := w.Project(ra, dec)
		if !ok {
			t.Fatal("near-pole round trip failed to project")
		}
		closeTo(t, gx, px[0], 1e-5, "near-pole x")
		closeTo(t, gy, px[1], 1e-5, "near-pole y")
	}
}

func TestProjectRejectsFarSide(t *testing.T) {
	w := testWCS(0, Normal)
	// Antipode of the field centre has no gnomonic projection.
	if _, _, ok := w.Project(w.RA0+180, -w.Dec0); ok {
		t.Error("expected the antipode to fail to project")
	}
}

func TestNewRejectsBadInput(t *testing.T) {
	cases := []struct {
		name              string
		ra, dec, pixScale float64
		w, h              int
	}{
		{"zero pixel scale", 10, 10, 0, 100, 100},
		{"negative pixel scale", 10, 10, -1, 100, 100},
		{"zero width", 10, 10, 1, 0, 100},
		{"zero height", 10, 10, 1, 100, 0},
		{"dec out of range", 10, 91, 1, 100, 100},
		{"NaN ra", math.NaN(), 10, 1, 100, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := New(tc.ra, tc.dec, tc.pixScale, 0, Normal, tc.w, tc.h); ok {
				t.Error("expected New to reject this input")
			}
		})
	}
}

func TestPixelDims(t *testing.T) {
	cases := []struct {
		name                        string
		widthAS, heightAS, pixScale float64
		wantW, wantH                int
		wantOK                      bool
	}{
		{"typical narrow field", 4000, 3000, 1.0, 4000, 3000, true},
		{"sub-arcsecond scale", 2683.6, 1789.1, 0.671, 3999, 2666, true},
		{"zero scale", 4000, 3000, 0, 0, 0, false},
		{"zero field", 0, 3000, 1, 0, 0, false},
		{"implausibly small", 4, 4, 1, 0, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h, ok := PixelDims(tc.widthAS, tc.heightAS, tc.pixScale)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && (w != tc.wantW || h != tc.wantH) {
				t.Errorf("dims = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestSeparation(t *testing.T) {
	cases := []struct {
		name                 string
		ra1, dec1, ra2, dec2 float64
		want                 float64
	}{
		{"one degree in dec", 0, 0, 0, 1, 1},
		{"one degree in ra at equator", 0, 0, 1, 0, 1},
		{"ra wrap", 359.5, 0, 0.5, 0, 1},
		{"antipodes", 0, -90, 180, 90, 180},
		{"identical", 123.4, -56.7, 123.4, -56.7, 0},
		// One degree of RA at dec -60 subtends half a degree on the sky.
		{"ra compressed by declination", 0, -60, 1, -60, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Separation(tc.ra1, tc.dec1, tc.ra2, tc.dec2)
			closeTo(t, got, tc.want, 1e-3, "separation")
		})
	}
}

func TestRadiusDegCoversCorners(t *testing.T) {
	w := testWCS(42, Normal)
	r := w.RadiusDeg()
	for _, px := range [][2]float64{{0, 0}, {1000, 0}, {0, 800}, {1000, 800}} {
		ra, dec := w.Deproject(px[0], px[1])
		if sep := Separation(w.RA0, w.Dec0, ra, dec); sep > r+1e-6 {
			t.Errorf("corner %v is %.6f deg from centre, beyond radius %.6f", px, sep, r)
		}
	}
}

// TestAgainstSolvedImages is the ground truth for this package.
//
// The expected directions were not derived from any documentation. They were
// read off the coordinate grids that Astrometry.net drew on the annotated
// JPEGs for three images in the site's own collection, by noting which way
// declination and right ascension increase across each frame. Three
// orientations that are nowhere near each other, north and east checked on
// every one.
//
// If FromAstrometry's conventions are ever "corrected" to match a plausible
// reading of the docs, this test is what says the docs lost.
func TestAgainstSolvedImages(t *testing.T) {
	cases := []struct {
		name string
		// as stored in the images table
		ra, dec, pixScale, orientation float64
		widthArcsec, heightArcsec      float64
		// unit vectors in image coordinates (x right, y down) that north and
		// east should point along, read off the annotated JPEG's grid
		northX, northY float64
		eastX, eastY   float64
	}{
		{
			// images/ic434_annotated.jpg — declination decreases left to right
			// and right ascension increases downward: north left, east down.
			name: "IC 434 Horsehead",
			ra:   85.300, dec: -2.217, pixScale: 1.141, orientation: 89.588,
			widthArcsec: 4571.7, heightArcsec: 3047.8,
			northX: -1, northY: 0,
			eastX: 0, eastY: 1,
		},
		{
			// images/ldn1634_annotated.jpg — declination increases downward and
			// right ascension increases to the right: north down, east right.
			name: "LDN 1634",
			ra:   79.941, dec: -5.869, pixScale: 0.671, orientation: 182.281,
			widthArcsec: 4163.5, heightArcsec: 2768.5,
			northX: 0, northY: 1,
			eastX: 1, eastY: 0,
		},
		{
			// images/ngc6611_annotated.jpg — the declination labels run toward
			// the lower left and the right ascension labels toward the lower
			// right, at roughly 55 degrees.
			name: "M16 Eagle",
			ra:   274.889, dec: -13.821, pixScale: 1.690, orientation: 124.682,
			widthArcsec: 5136.3, heightArcsec: 3403.9,
			northX: -0.822, northY: 0.569,
			eastX: 0.569, eastY: 0.822,
		},
	}

	// Grid labels can only be read off a JPEG so precisely; three degrees is
	// tight enough to exclude every wrong convention (the alternatives are all
	// at least 90 degrees away) and loose enough to survive the eyeballing.
	const tolDeg = 3.0

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pw, ph, ok := PixelDims(tc.widthArcsec, tc.heightArcsec, tc.pixScale)
			if !ok {
				t.Fatal("could not derive pixel dimensions")
			}
			// Parity is unknown for these rows, which is exactly the situation
			// in the database, so exercise the default.
			w, ok := FromAstrometry(tc.ra, tc.dec, tc.pixScale, tc.orientation, 1, pw, ph)
			if !ok {
				t.Fatal("FromAstrometry rejected a real solved image")
			}

			cx, cy := float64(pw)/2, float64(ph)/2
			check := func(what string, paDeg, wantX, wantY float64) {
				t.Helper()
				// One arcminute out from the centre along the given position
				// angle, which is small enough to stay linear.
				ra, dec := offsetSky(tc.ra, tc.dec, 60, paDeg)
				x, y, ok := w.Project(ra, dec)
				if !ok {
					t.Fatalf("%s point failed to project", what)
				}
				dx, dy := x-cx, y-cy
				n := math.Hypot(dx, dy)
				if n == 0 {
					t.Fatalf("%s produced no offset", what)
				}
				dx, dy = dx/n, dy/n

				// Angle between the measured and expected directions.
				dot := dx*wantX + dy*wantY
				angle := math.Acos(math.Max(-1, math.Min(1, dot))) * 180 / math.Pi
				if angle > tolDeg {
					t.Errorf("%s points (%.3f, %.3f), want (%.3f, %.3f) — off by %.1f deg",
						what, dx, dy, wantX, wantY, angle)
				}
			}
			check("north", 0, tc.northX, tc.northY)
			check("east", 90, tc.eastX, tc.eastY)
		})
	}
}

func TestFromAstrometryParity(t *testing.T) {
	dims := func(p float64) WCS {
		w, ok := FromAstrometry(80, -69, 1, 30, p, 1000, 800)
		if !ok {
			t.Fatal("FromAstrometry rejected valid input")
		}
		return w
	}
	if got := dims(1).Parity; got != Normal {
		t.Errorf("astrometry parity +1 = %d, want Normal (%d)", got, Normal)
	}
	if got := dims(-1).Parity; got != Mirrored {
		t.Errorf("astrometry parity -1 = %d, want Mirrored (%d)", got, Mirrored)
	}
	// The orientation is negated on the way in.
	if got := dims(1).Orientation; got != -30 {
		t.Errorf("orientation = %g, want -30", got)
	}
}

func TestContains(t *testing.T) {
	w := testWCS(0, Normal)
	cases := []struct {
		name   string
		x, y   float64
		margin float64
		want   bool
	}{
		{"centre", 500, 400, 0, true},
		{"top left corner", 0, 0, 0, true},
		{"just outside", -1, 400, 0, false},
		{"outside but within margin", -1, 400, 5, true},
		{"beyond margin", -10, 400, 5, false},
		{"past right edge", 1001, 400, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.Contains(tc.x, tc.y, tc.margin); got != tc.want {
				t.Errorf("Contains(%g, %g, %g) = %v, want %v", tc.x, tc.y, tc.margin, got, tc.want)
			}
		})
	}
}
