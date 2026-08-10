package catalog

import (
	"math"
	"testing"

	"deepspaceplace/internal/wcs"
)

func TestEmbeddedCatalogLoads(t *testing.T) {
	if n := Len(); n < 90000 {
		t.Fatalf("catalogue holds %d objects, expected the full ~98k — is data/catalog.tsv.gz stale?", n)
	}
}

// find looks an object up by primary designation within a generous cone.
func find(t *testing.T, ra, dec, radius float64, id string) (Object, bool) {
	t.Helper()
	for _, o := range Query(ra, dec, radius) {
		if o.ID == id {
			return o, true
		}
	}
	return Object{}, false
}

// TestKnownObjects pins a handful of objects across all three layers and both
// hemispheres, with positions checked against their catalogue values. If an
// ingest change silently shifts coordinates — a bad sexagesimal parse, an
// hours-versus-degrees slip in the star data — this is what catches it.
func TestKnownObjects(t *testing.T) {
	cases := []struct {
		id       string
		kind     Kind
		ra, dec  float64
		common   string
		wantSize bool
	}{
		{id: "NGC 2070", kind: DSO, ra: 84.6765, dec: -69.1009, wantSize: true},
		{id: "M 42", kind: DSO, ra: 83.8187, dec: -5.3897, common: "Great Orion Nebula", wantSize: true},
		{id: "M 8", kind: DSO, ra: 270.9220, dec: -24.3802, common: "Lagoon Nebula", wantSize: true},
		{id: "NGC 3372", kind: DSO, ra: 161.2855, dec: -59.8667, common: "Carina Nebula", wantSize: true},
		{id: "NGC 253", kind: DSO, ra: 11.8880, dec: -25.2882, wantSize: true},
		{id: "Sh2-155", kind: Emission, ra: 344.1826, dec: 62.6176, wantSize: true},
		{id: "RCW 49", kind: Emission, ra: 155.9605, dec: -57.7038, wantSize: true},
		{id: "LBN 1039", kind: Emission, ra: 106.3327, dec: -12.2602, wantSize: true},
		{id: "Henize 206", kind: Emission, ra: 82.9833, dec: -71.0053, wantSize: true},
		{id: "Alnitak", kind: Star, ra: 85.1897, dec: -1.9426},
		{id: "Achernar", kind: Star, ra: 24.4283, dec: -57.2368},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			o, ok := find(t, tc.ra, tc.dec, 0.5, tc.id)
			if !ok {
				t.Fatalf("%s not found within 0.5 deg of its catalogue position", tc.id)
			}
			if o.Kind != tc.kind {
				t.Errorf("kind = %q, want %q", o.Kind, tc.kind)
			}
			if sep := wcs.Separation(tc.ra, tc.dec, o.RA, o.Dec); sep > 1.0/60 {
				t.Errorf("position off by %.4f deg", sep)
			}
			if tc.common != "" && o.Common != tc.common {
				t.Errorf("common = %q, want %q", o.Common, tc.common)
			}
			if tc.wantSize && o.MajAx <= 0 {
				t.Errorf("expected a non-zero angular size, got %g", o.MajAx)
			}
			if tc.kind == Star && !o.HasMag {
				t.Error("expected a star to carry a magnitude")
			}
		})
	}
}

// A star catalogue built with right ascension in hours rather than degrees
// still passes a coarse smoke test, so check a star whose RA is large enough
// that the two would be unmistakably different.
func TestStarRightAscensionIsDegrees(t *testing.T) {
	// Betelgeuse sits at RA 88.79 deg = 5.92 h.
	o, ok := find(t, 88.7929, 7.4071, 0.2, "Betelgeuse")
	if !ok {
		t.Fatal("Betelgeuse not found at its degree-valued position")
	}
	if o.RA < 80 {
		t.Errorf("RA = %g, looks like hours rather than degrees", o.RA)
	}
}

func TestQueryRadius(t *testing.T) {
	const ra, dec = 84.6765, -69.1009 // the Tarantula

	small := Query(ra, dec, 0.2)
	large := Query(ra, dec, 2.0)
	if len(small) == 0 {
		t.Fatal("expected objects in the LMC within 0.2 deg")
	}
	if len(large) <= len(small) {
		t.Errorf("a 2 deg cone returned %d objects, not more than the %d in 0.2 deg", len(large), len(small))
	}

	// Nothing may lie beyond the requested radius once its own extent is
	// allowed for.
	for _, o := range large {
		if sep := wcs.Separation(ra, dec, o.RA, o.Dec); sep > 2.0+o.RadiusDeg()+1e-9 {
			t.Errorf("%s is %.4f deg away with radius %.4f, outside the 2 deg cone",
				o.ID, sep, o.RadiusDeg())
		}
	}
}

// The declination band that Query uses to narrow the search must not cause it
// to miss objects that sit across the 0/360 right ascension seam.
func TestQueryAcrossRAWrap(t *testing.T) {
	got := Query(0.2, -20, 1.5)
	var wrapped int
	for _, o := range got {
		if o.RA > 180 {
			wrapped++
		}
	}
	if wrapped == 0 {
		t.Error("expected the cone at RA 0.2 to include objects just below RA 360")
	}
	for _, o := range got {
		if sep := wcs.Separation(0.2, -20, o.RA, o.Dec); sep > 1.5+o.RadiusDeg()+1e-9 {
			t.Errorf("%s at RA %g is %.4f deg away, outside the cone", o.ID, o.RA, sep)
		}
	}
}

// TestQueryFindsOverlappingGiants is the regression guard for a real miss: the
// Prawn Nebula field returned no emission nebulosity at all, because RCW 113 is
// six degrees across and centred well outside it. Matching object centres
// against the field radius loses exactly the large southern complexes that
// matter most here.
func TestQueryFindsOverlappingGiants(t *testing.T) {
	// The site's IC 4628 frame: centre and its circumscribed radius.
	const ra, dec, radius = 254.254, -40.343, 1.04

	var found *Object
	for _, o := range Query(ra, dec, radius) {
		if o.ID == "RCW 113" {
			found = &o
			break
		}
	}
	if found == nil {
		t.Fatal("RCW 113 not found: a six degree nebula covering this field was missed")
	}

	// Confirm it really is the far-centred case, so this test keeps testing
	// what it is named for.
	sep := wcs.Separation(ra, dec, found.RA, found.Dec)
	if sep <= radius {
		t.Fatalf("RCW 113's centre is only %.2f deg away, inside the %.2f deg field — "+
			"this no longer exercises the overlap path", sep, radius)
	}
	if sep > radius+found.RadiusDeg() {
		t.Errorf("RCW 113 is %.2f deg away but only %.2f deg in radius; it should not have matched",
			sep, found.RadiusDeg())
	}
}

// Nothing may match whose extent falls short of the search circle.
func TestQueryExcludesNonOverlapping(t *testing.T) {
	const ra, dec, radius = 254.254, -40.343, 1.04
	for _, o := range Query(ra, dec, radius) {
		sep := wcs.Separation(ra, dec, o.RA, o.Dec)
		if sep > radius+o.RadiusDeg()+1e-9 {
			t.Errorf("%s is %.3f deg away with radius %.3f, beyond the %.2f deg field",
				o.ID, sep, o.RadiusDeg(), radius)
		}
	}
}

func TestQueryNearPole(t *testing.T) {
	// A cone that swallows the south celestial pole must not panic and must
	// still respect its radius.
	got := Query(0, -89.5, 2.0)
	for _, o := range got {
		if sep := wcs.Separation(0, -89.5, o.RA, o.Dec); sep > 2.0+o.RadiusDeg()+1e-9 {
			t.Errorf("%s is %.4f deg away, outside the cone", o.ID, sep)
		}
	}
}

func TestQueryEmptyRegion(t *testing.T) {
	if got := Query(180, 0, 0); got != nil {
		t.Errorf("a zero radius returned %d objects, want none", len(got))
	}
	if got := Query(180, 0, -1); got != nil {
		t.Errorf("a negative radius returned %d objects, want none", len(got))
	}
}

func TestLabel(t *testing.T) {
	cases := []struct {
		name string
		obj  Object
		want string
	}{
		{"prefers the common name", Object{ID: "M 42", Common: "Great Orion Nebula"}, "Great Orion Nebula"},
		{"falls back to the designation", Object{ID: "Sh2-155"}, "Sh2-155"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.obj.Label(); got != tc.want {
				t.Errorf("Label() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllPositionsAreSane(t *testing.T) {
	for _, o := range Query(0, 0, 180) {
		if math.IsNaN(o.RA) || o.RA < 0 || o.RA >= 360 {
			t.Fatalf("%s has RA %g outside [0, 360)", o.ID, o.RA)
		}
		if math.IsNaN(o.Dec) || o.Dec < -90 || o.Dec > 90 {
			t.Fatalf("%s has Dec %g outside [-90, 90]", o.ID, o.Dec)
		}
		if o.ID == "" {
			t.Fatal("found an object with no designation")
		}
	}
}
