package handlers

import "testing"

// splitCatalogID underpins the zero-padding half of legacy id recovery. The
// interesting cases are the ids it must refuse to split, because a bad split
// would generate padding candidates for something that was never a catalog id.
func TestSplitCatalogID(t *testing.T) {
	cases := []struct {
		id                     string
		prefix, digits, suffix string
		ok                     bool
	}{
		{"ngc253b", "ngc", "253", "b", true},
		{"ngc253", "ngc", "253", "", true},
		{"m42", "m", "42", "", true},
		{"rcw7", "rcw", "7", "", true},
		{"ic434", "ic", "434", "", true},
		{"ngc3372stl2", "ngc", "3372", "stl2", true},
		{"n70b", "n", "70", "b", true},

		// Already padded -- harmless, just widens from here.
		{"ngc0253b", "ngc", "0253", "b", true},

		// Not prefix-digits shaped. These must not be split, so they generate
		// no candidates and fall through to a 404.
		{"moon", "", "", "", false},
		{"", "", "", "", false},
		{"253", "", "", "", false}, // no alpha prefix
		{"velasnr", "", "", "", false},

		// sh2-280 splits to prefix "sh", which produces candidates like
		// sh02-280 that are not real ids -- so it resolves to nothing rather
		// than to the wrong image.
		{"sh2-280", "sh", "2", "-280", true},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			prefix, digits, suffix, ok := splitCatalogID(tc.id)
			if ok != tc.ok || prefix != tc.prefix || digits != tc.digits || suffix != tc.suffix {
				t.Errorf("splitCatalogID(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)",
					tc.id, prefix, digits, suffix, ok, tc.prefix, tc.digits, tc.suffix, tc.ok)
			}
		})
	}
}

// The padding candidates resolveLegacyID tries must widen from the requested
// id, never narrow, and never reorder the suffix -- otherwise a legacy id could
// redirect to an unrelated image.
func TestPaddingCandidatesOnlyAddZeros(t *testing.T) {
	prefix, digits, suffix, ok := splitCatalogID("rcw7")
	if !ok {
		t.Fatal("rcw7 should split")
	}
	want := []string{"rcw07", "rcw007", "rcw0007", "rcw00007"}
	var got []string
	for width := len(digits) + 1; width <= 5; width++ {
		got = append(got, prefix+repeatZero(width-len(digits))+digits+suffix)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func repeatZero(n int) string {
	s := ""
	for i := 0; i < n; i++ {
		s += "0"
	}
	return s
}
