// Package catalog provides an embedded deep-sky, emission-nebula and star
// catalogue with a cone search over it.
//
// The data file is produced by cmd/catalogtool and committed, so the site
// builds offline and every deployment ships an identical catalogue. See that
// command's documentation for the sources and how to regenerate.
package catalog

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"sort"
	"strconv"
	"strings"
	"sync"

	"deepspaceplace/internal/wcs"
)

//go:embed data/catalog.tsv.gz
var packed []byte

// Kind distinguishes the annotation layers, which are toggled independently.
type Kind byte

const (
	DSO      Kind = 'd' // NGC, IC, Messier and the OpenNGC addendum
	Emission Kind = 'e' // Sharpless, RCW, LBN, Henize
	Star     Kind = 's'
)

// Object is one catalogue entry. Angular sizes are in arcminutes and the
// position angle in degrees; all three are zero for stars and for objects whose
// source catalogue records no size.
type Object struct {
	Kind    Kind
	ID      string // primary designation, e.g. "NGC 2070"
	Common  string // common name, e.g. "Tarantula Nebula"
	Aliases string // comma-separated alternate designations
	RA, Dec float64
	MajAx   float64
	MinAx   float64
	PA      float64
	Mag     float64
	HasMag  bool
}

// Label is the best short name for the object: the common name when there is
// one, otherwise the designation.
func (o Object) Label() string {
	if o.Common != "" {
		return o.Common
	}
	return o.ID
}

// objects is sorted by declination, which is what makes the cone search cheap.
var objects = sync.OnceValue(load)

// maxRadiusDeg is the angular radius of the largest object in the catalogue.
// Query widens its declination band by this much so that objects big enough to
// cover a field from outside it are still found -- several RCW and LBN nebulae
// are degrees across, and searching on centres alone silently loses them.
var maxRadiusDeg = sync.OnceValue(func() float64 {
	var max float64
	for _, o := range objects() {
		if r := o.RadiusDeg(); r > max {
			max = r
		}
	}
	return max
})

// RadiusDeg is half the object's major axis, in degrees. Zero for stars and for
// objects whose catalogue records no size.
func (o Object) RadiusDeg() float64 { return o.MajAx / 2 / 60 }

func load() []Object {
	zr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		// The data is embedded at build time, so a failure here means the
		// binary itself is corrupt. Degrade to no annotations rather than
		// taking the whole site down.
		return nil
	}
	defer zr.Close()

	var out []Object
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 10 || f[0] == "" {
			continue
		}
		mag, hasMag := parseFloat(f[9])
		ra, okRA := parseFloat(f[4])
		dec, okDec := parseFloat(f[5])
		if !okRA && f[4] != "" || !okDec && f[5] != "" {
			continue
		}
		majAx, _ := parseFloat(f[6])
		minAx, _ := parseFloat(f[7])
		pa, _ := parseFloat(f[8])

		out = append(out, Object{
			Kind:    Kind(f[0][0]),
			ID:      f[1],
			Common:  f[2],
			Aliases: f[3],
			RA:      ra,
			Dec:     dec,
			MajAx:   majAx,
			MinAx:   minAx,
			PA:      pa,
			Mag:     mag,
			HasMag:  hasMag,
		})
	}
	return out
}

// parseFloat treats the empty string as an absent value rather than an error,
// since the data file omits zeros to stay small.
func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// Query returns every catalogue object that overlaps the circle of the given
// radius around a position -- that is, every object whose own extent comes
// within radiusDeg of it, not merely those whose centre does.
//
// The distinction matters: the Prawn Nebula's field sits inside RCW 113, a six
// degree complex centred more than two degrees away. Matching on centres alone
// would report no emission nebulosity there at all.
//
// Results are unordered; callers that need to cap the number of labels should
// sort by whatever they consider important.
func Query(ra, dec, radiusDeg float64) []Object {
	if radiusDeg <= 0 {
		return nil
	}
	all := objects()

	// Declination is a lower bound on true angular separation, so a band of
	// +/- (radius + the largest object's own radius) is guaranteed to contain
	// every match. Near a pole the band simply runs off the end of the slice,
	// which is harmless.
	band := radiusDeg + maxRadiusDeg()
	lo := sort.Search(len(all), func(i int) bool { return all[i].Dec >= dec-band })
	hi := dec + band

	var out []Object
	for i := lo; i < len(all) && all[i].Dec <= hi; i++ {
		o := all[i]
		if wcs.Separation(ra, dec, o.RA, o.Dec) <= radiusDeg+o.RadiusDeg() {
			out = append(out, o)
		}
	}
	return out
}

// Len reports how many objects the embedded catalogue holds. Used by tests and
// worth logging at startup as a smoke check on the embedded data.
func Len() int { return len(objects()) }
