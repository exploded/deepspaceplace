package handlers

import (
	"database/sql"
	"testing"

	"deepspaceplace/internal/database"
)

func nf(v float64) sql.NullFloat64 { return sql.NullFloat64{Float64: v, Valid: true} }

// TestScaleBoundsCoverRealFailures is the regression guard for the two images
// that would not solve. Both are downscaled, so their true pixel scale is far
// coarser than the camera's native figure, and the old fixed +/-30% window
// around that figure excluded the right answer outright.
func TestScaleBoundsCoverRealFailures(t *testing.T) {
	cases := []struct {
		name      string
		filename  string
		fileW     int
		widthAS   float64
		camera    string
		scope     string
		trueScale float64 // arcsec/pixel the solver actually needs to find
	}{
		{
			name: "m45, halved from native", filename: "m45.jpg",
			fileW: 1920, widthAS: 8064,
			camera: "STL-11000M", scope: "AT8 IN",
			trueScale: 4.2,
		},
		{
			name: "ngc1871, downscaled and cropped", filename: "ngc1871.jpg",
			fileW: 2473, widthAS: 6120,
			camera: "STL-11000M", scope: "AT12 IN",
			trueScale: 2.475,
		},
		{
			name: "ic434, near native resolution", filename: "ic434.jpg",
			fileW: 3840, widthAS: 4571.7,
			camera: "STL-11000M", scope: "GSO 8 RC",
			trueScale: 1.19,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixtureImage(t, dir, tc.filename, tc.fileW, tc.fileW*2/3)
			old := ImagesDir
			ImagesDir = dir
			t.Cleanup(func() { ImagesDir = old })

			img := database.Image{
				Filename:    tc.filename,
				Camera:      tc.camera,
				Scope:       tc.scope,
				WidthArcsec: nf(tc.widthAS),
			}
			got := scaleBounds(img)

			if got.Lower <= 0 || got.Upper <= got.Lower {
				t.Fatalf("no usable range: %+v", got)
			}
			if tc.trueScale < got.Lower || tc.trueScale > got.Upper {
				t.Errorf("true scale %.3f is outside the submitted range %.3f-%.3f — "+
					"the solver would be told to look where the answer is not",
					tc.trueScale, got.Lower, got.Upper)
			}
		})
	}
}

// Without a recorded field width there is nothing to measure against, so the
// camera lookup has to carry the estimate -- and it must be widened, because
// the file may since have been downscaled.
func TestScaleBoundsFallsBackToCameraLookup(t *testing.T) {
	dir := t.TempDir()
	old := ImagesDir
	ImagesDir = dir
	t.Cleanup(func() { ImagesDir = old })

	img := database.Image{
		Filename: "missing.jpg", // deliberately not on disk
		Camera:   "STL-11000M",
		Scope:    "AT8 IN",
	}
	got := scaleBounds(img)

	if got.Lower <= 0 {
		t.Fatalf("expected a range from the camera lookup, got %+v", got)
	}
	// The native figure for this pairing is 2.0; a halved file needs 4.0.
	for _, scale := range []float64{2.0, 4.0} {
		if scale < got.Lower || scale > got.Upper {
			t.Errorf("%.1f arcsec/px is outside %.3f-%.3f", scale, got.Lower, got.Upper)
		}
	}
}

func TestScaleBoundsUnknownCameraSolvesBlind(t *testing.T) {
	dir := t.TempDir()
	old := ImagesDir
	ImagesDir = dir
	t.Cleanup(func() { ImagesDir = old })

	got := scaleBounds(database.Image{Filename: "missing.jpg", Camera: "Some Phone", Scope: "Tripod"})
	if got.Lower != 0 {
		t.Errorf("expected no constraint for an unknown camera, got %+v", got)
	}
}

// TestOverlaySurvivesFailedResolve covers the interaction between the two
// changes made here: failed solves now preserve the astrometry columns, so an
// image whose most recent attempt failed must keep the overlay its still-valid
// coordinates earn.
func TestOverlaySurvivesFailedResolve(t *testing.T) {
	withFixtures(t, 3840, 2560)

	img := solvedImage()
	img.Solved = "f"

	o := BuildOverlay(img)
	if o == nil {
		t.Fatal("a failed re-solve hid the overlay, though the stored solution is still good")
	}
	if len(o.Objects) == 0 {
		t.Error("expected catalogue objects")
	}
}

// A failed solve on data that does not describe the image is still refused, by
// the geometry check rather than the status flag.
func TestOverlayStillRefusesBadGeometry(t *testing.T) {
	withFixtures(t, 1920, 1080) // 16:9, unlike the recorded 3:2 field

	img := solvedImage()
	img.Solved = "f"

	if o := BuildOverlay(img); o != nil {
		t.Errorf("expected no overlay for a mismatched field, got %d objects", len(o.Objects))
	}
}
