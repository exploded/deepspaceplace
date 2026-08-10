package handlers

import (
	"database/sql"
	"html/template"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deepspaceplace/internal/database"
)

// writeFixtureImage creates a JPEG of the given size so imageDims has something
// real to measure.
func writeFixtureImage(t *testing.T, dir, name string, w, h int) {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.Gray{Y: 255})

	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("creating fixture image: %v", err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, nil); err != nil {
		t.Fatalf("encoding fixture image: %v", err)
	}
}

// solvedImage is the Horsehead row as it actually appears in the database,
// which is a real, correct solve.
func solvedImage() database.Image {
	f := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	return database.Image{
		ID:           "ic0434",
		Filename:     "ic434.jpg",
		Solved:       "y",
		Ra:           f(85.300),
		Dec:          f(-2.217),
		Pixscale:     f(1.141),
		Orientation:  f(89.588),
		WidthArcsec:  f(4571.7),
		HeightArcsec: f(3047.8),
	}
}

func withFixtures(t *testing.T, w, h int) {
	t.Helper()
	dir := t.TempDir()
	writeFixtureImage(t, dir, "ic434.jpg", w, h)

	old := ImagesDir
	ImagesDir = dir
	t.Cleanup(func() { ImagesDir = old })
}

func TestBuildOverlayOnSolvedImage(t *testing.T) {
	withFixtures(t, 3840, 2560)

	o := BuildOverlay(solvedImage())
	if o == nil {
		t.Fatal("expected an overlay for a correctly solved image")
	}
	if o.W != 3840 || o.H != 2560 {
		t.Errorf("viewBox is %dx%d, want the image's own 3840x2560", o.W, o.H)
	}
	if len(o.Objects) == 0 {
		t.Error("expected catalogue objects in the Horsehead field")
	}
	if len(o.GridLines) == 0 {
		t.Error("expected coordinate grid lines")
	}
	if o.ScaleBar == nil {
		t.Error("expected a scale bar")
	}
	if o.Compass == nil {
		t.Error("expected a compass")
	}

	// Everything drawn has to actually reach the picture; a sign error in the
	// projection would fling objects clear of it. Large objects may be centred
	// well outside the frame and still belong, but only as far outside as their
	// own radius carries them back onto it.
	const margin = 200
	for _, obj := range o.Objects {
		reach := obj.RX + margin
		if obj.Point {
			reach = obj.PointR + margin
		}
		if obj.X < -reach || obj.X > 3840+reach || obj.Y < -reach || obj.Y > 2560+reach {
			t.Errorf("%q at (%.0f, %.0f) with radius %.0f never reaches the frame",
				obj.Label, obj.X, obj.Y, obj.RX)
		}
	}

	// The field is centred on the Horsehead, so its neighbours must be here.
	var labels []string
	for _, obj := range o.Objects {
		labels = append(labels, obj.Label, obj.Sub)
	}
	joined := strings.Join(labels, "|")
	for _, want := range []string{"IC 434", "NGC 2024", "NGC 2023", "B 33"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q among the annotations, got: %s", want, joined)
		}
	}
}

// TestBuildOverlayRejects covers every route to "no overlay". Each of these
// would otherwise draw annotations in confidently wrong places.
func TestBuildOverlayRejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*database.Image)
		// dimensions of the fixture image on disk
		w, h int
	}{
		{
			name:   "no coordinates",
			mutate: func(i *database.Image) { i.Ra = sql.NullFloat64{} },
			w:      3840, h: 2560,
		},
		{
			name:   "no declination",
			mutate: func(i *database.Image) { i.Dec = sql.NullFloat64{} },
			w:      3840, h: 2560,
		},
		{
			name:   "no field size",
			mutate: func(i *database.Image) { i.WidthArcsec = sql.NullFloat64{} },
			w:      3840, h: 2560,
		},
		// A solved value of "f" is deliberately absent here: a failed attempt no
		// longer hides a still-valid solution. See TestOverlaySurvivesFailedResolve.
		{
			name:   "image file missing",
			mutate: func(i *database.Image) { i.Filename = "nonexistent.jpg" },
			w:      3840, h: 2560,
		},
		{
			name:   "filename escaping the images directory",
			mutate: func(i *database.Image) { i.Filename = "../secrets.jpg" },
			w:      3840, h: 2560,
		},
		{
			// A hand-typed field size that does not describe this image at all.
			// Several rows in the real database look like this.
			name:   "solved shape disagrees with the image",
			mutate: func(i *database.Image) {},
			w:      3840, h: 3840,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withFixtures(t, tc.w, tc.h)
			img := solvedImage()
			tc.mutate(&img)
			if o := BuildOverlay(img); o != nil {
				t.Errorf("expected no overlay, got one with %d objects", len(o.Objects))
			}
		})
	}
}

// The stored pixel scale describes the image as solved, but the admin resizer
// rewrites files in place at up to 3840px. Deriving the scale from the file's
// actual width is what keeps the overlay registered afterwards.
func TestBuildOverlaySurvivesDownscaling(t *testing.T) {
	full := func() *Overlay {
		withFixtures(t, 3840, 2560)
		return BuildOverlay(solvedImage())
	}()
	half := func() *Overlay {
		withFixtures(t, 1920, 1280)
		return BuildOverlay(solvedImage())
	}()

	if full == nil || half == nil {
		t.Fatal("expected overlays at both sizes")
	}
	if half.W != 1920 || half.H != 1280 {
		t.Fatalf("downscaled viewBox is %dx%d, want 1920x1280", half.W, half.H)
	}
	if len(full.Objects) != len(half.Objects) {
		t.Fatalf("object counts differ: %d at full size, %d at half", len(full.Objects), len(half.Objects))
	}

	// Positions should track the resize exactly: the same sky in half the pixels.
	for i := range full.Objects {
		wantX, wantY := full.Objects[i].X/2, full.Objects[i].Y/2
		if diff := half.Objects[i].X - wantX; diff > 1 || diff < -1 {
			t.Errorf("%s x = %.1f, want about %.1f", half.Objects[i].Label, half.Objects[i].X, wantX)
		}
		if diff := half.Objects[i].Y - wantY; diff > 1 || diff < -1 {
			t.Errorf("%s y = %.1f, want about %.1f", half.Objects[i].Label, half.Objects[i].Y, wantY)
		}
	}
}

func TestFormatDec(t *testing.T) {
	cases := []struct {
		name string
		dec  float64
		step float64
		want string
	}{
		{"whole degrees", -2.0, 3600, "−2°"},
		{"degrees and minutes", -2.25, 60, "−2° 15′"},
		{"northern hemisphere", 62.5, 60, "+62° 30′"},
		// Values are snapped to the grid spacing, so 2° 15′ 09″ shown on a
		// 15-arcsecond grid reads as 2° 15′ 15″ rather than drifting off it.
		{"snaps to the grid step", -2.2525, 15, "−2° 15′ 15″"},
		{"exact on a 15 arcsecond step", -2.254166, 15, "−2° 15′ 15″"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatDec(tc.dec, tc.step); got != tc.want {
				t.Errorf("formatDec(%g, %g) = %q, want %q", tc.dec, tc.step, got, tc.want)
			}
		})
	}
}

func TestFormatRA(t *testing.T) {
	cases := []struct {
		name string
		ra   float64
		step float64
		want string
	}{
		// Steps are in arcseconds of angle but right ascension is labelled in
		// time, so a one-degree step is four minutes of time and has to show
		// minutes to stay distinguishable.
		{"one degree step shows minutes", 85.0, 3600, "5h 40m"},
		{"one arcminute step shows seconds", 85.25, 60, "5h 41m 00s"},
		{"with seconds", 85.3, 15, "5h 41m 12s"},
		// Just short of a full turn must not render as hour 24.
		{"wraps below 24h", 359.999, 3600, "0h 00m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatRA(tc.ra, tc.step); got != tc.want {
				t.Errorf("formatRA(%g, %g) = %q, want %q", tc.ra, tc.step, got, tc.want)
			}
		})
	}
}

// TestAdminEditTemplateRenders guards the parity control on the edit form. Its
// selected-option logic compares a float64 against a constant, which
// html/template only type-checks when the template actually runs -- so nothing
// short of executing it proves the page still loads.
func TestAdminEditTemplateRenders(t *testing.T) {
	tmpl, err := template.New("edit.html").Funcs(TemplateFuncs).
		ParseFiles(filepath.Join("..", "templates", "admin", "edit.html"))
	if err != nil {
		t.Fatalf("parsing the admin edit template: %v", err)
	}

	f := func(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }
	cases := []struct {
		name       string
		parity     sql.NullFloat64
		wantChosen string
	}{
		{"unset parity", sql.NullFloat64{}, `value="" selected`},
		{"normal parity", f(1), `value="1" selected`},
		{"mirrored parity", f(-1), `value="-1" selected`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img := solvedImage()
			img.Parity = tc.parity

			// Mirrors the view model HandleAdminEdit renders with.
			data := struct {
				PageData
				database.Image
				CSRFToken string
			}{Image: img, CSRFToken: "test-token"}

			// The file is nothing but {{define}} blocks, so the form lives in
			// "content" rather than at the template's top level.
			var out strings.Builder
			if err := tmpl.ExecuteTemplate(&out, "content", data); err != nil {
				t.Fatalf("executing the admin edit template: %v", err)
			}
			if !strings.Contains(out.String(), tc.wantChosen) {
				t.Errorf("expected the %s option to be selected", tc.name)
			}
		})
	}
}
