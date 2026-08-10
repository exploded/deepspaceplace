// Command catalogtool distils the raw astronomical catalogues into the single
// compact file that internal/catalog embeds.
//
// It is run by hand, not by the build, and its output is committed — the site
// must build offline and reproducibly, and these source catalogues change
// rarely (most of them have not changed since the 1960s).
//
// Fetch the sources first:
//
//	curl -o NGC.csv      https://raw.githubusercontent.com/mattiaverga/OpenNGC/master/database_files/NGC.csv
//	curl -o addendum.csv https://raw.githubusercontent.com/mattiaverga/OpenNGC/master/database_files/addendum.csv
//	curl -o hyg.csv      https://raw.githubusercontent.com/astronexus/HYG-Database/main/hyg/CURRENT/hygdata_v41.csv
//	curl -o sh2.tsv "https://vizier.cds.unistra.fr/viz-bin/asu-tsv?-source=VII/20/catalog&-out.max=unlimited&-out.add=_RAJ2000,_DEJ2000&-oc.form=d&-out=Sh2,Diam"
//	curl -o rcw.tsv "https://vizier.cds.unistra.fr/viz-bin/asu-tsv?-source=VII/216/rcw&-out.max=unlimited&-out.add=_RAJ2000,_DEJ2000&-oc.form=d&-out=RCW,MajAxis,MinAxis,IDs"
//	curl -o lbn.tsv "https://vizier.cds.unistra.fr/viz-bin/asu-tsv?-source=VII/9/catalog&-out.max=unlimited&-out.add=_RAJ2000,_DEJ2000&-oc.form=d&-out=Seq,Diam1,Diam2,Name"
//
// Then:
//
//	go run ./cmd/catalogtool -src <dir> -out internal/catalog/data/catalog.tsv.gz
package main

import (
	"compress/gzip"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// record is one row of the distilled catalogue.
type record struct {
	Kind    string // "d" deep-sky, "e" emission nebula, "s" star
	ID      string // primary designation, e.g. "NGC 2070"
	Common  string // common name, e.g. "Tarantula Nebula"
	Aliases string // comma-separated alternate designations
	RA, Dec float64
	MajAx   float64 // arcmin
	MinAx   float64 // arcmin
	PA      float64 // degrees
	Mag     float64
	HasMag  bool
}

func main() {
	src := flag.String("src", ".", "directory holding the downloaded source catalogues")
	out := flag.String("out", "internal/catalog/data/catalog.tsv.gz", "output path")
	magLimit := flag.Float64("maglimit", 9.0, "faintest star magnitude to include")
	flag.Parse()

	var recs []record
	add := func(name string, load func() ([]record, error)) {
		got, err := load()
		if err != nil {
			log.Fatalf("%s: %v", name, err)
		}
		log.Printf("%-12s %6d objects", name, len(got))
		recs = append(recs, got...)
	}

	add("OpenNGC", func() ([]record, error) { return readOpenNGC(filepath.Join(*src, "NGC.csv")) })
	add("addendum", func() ([]record, error) { return readOpenNGC(filepath.Join(*src, "addendum.csv")) })
	add("Sharpless", func() ([]record, error) { return readSharpless(filepath.Join(*src, "sh2.tsv")) })
	add("RCW", func() ([]record, error) { return readRCW(filepath.Join(*src, "rcw.tsv")) })
	add("LBN", func() ([]record, error) { return readLBN(filepath.Join(*src, "lbn.tsv")) })
	add("HYG stars", func() ([]record, error) { return readHYG(filepath.Join(*src, "hyg.csv"), *magLimit) })
	add("extras", func() ([]record, error) { return extras(), nil })

	// Sorted by declination so the loader can binary-search a declination band.
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Dec != recs[j].Dec {
			return recs[i].Dec < recs[j].Dec
		}
		return recs[i].ID < recs[j].ID
	})

	if err := write(*out, recs); err != nil {
		log.Fatal(err)
	}
	fi, _ := os.Stat(*out)
	log.Printf("wrote %s: %d objects, %.1f KiB", *out, len(recs), float64(fi.Size())/1024)
}

func write(path string, recs []record) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	zw, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	defer zw.Close()

	fmt.Fprintln(zw, "# kind\tid\tcommon\taliases\tra\tdec\tmajax\tminax\tpa\tmag")
	for _, r := range recs {
		mag := ""
		if r.HasMag {
			mag = trimNum(r.Mag, 2)
		}
		fmt.Fprintf(zw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Kind, r.ID, r.Common, r.Aliases,
			trimNum(r.RA, 5), trimNum(r.Dec, 5),
			trimNum(r.MajAx, 2), trimNum(r.MinAx, 2), trimNum(r.PA, 1), mag)
	}
	return nil
}

// trimNum formats without trailing zeros, which shaves a useful fraction off
// the compressed size across ~100k rows.
func trimNum(v float64, prec int) string {
	if v == 0 {
		return ""
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// --- OpenNGC (NGC/IC and the addendum of well-known non-NGC objects) ---

// designation splits a compact catalogue name like "NGC0224A" into a spaced,
// zero-stripped form like "NGC 224A".
var designation = regexp.MustCompile(`^([A-Za-z]+)0*(\d+)(.*)$`)

func formatDesignation(name string) string {
	m := designation.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return strings.TrimSpace(name)
	}
	return m[1] + " " + m[2] + m[3]
}

func readOpenNGC(path string) ([]record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.Comma = ';'
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1

	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("no data rows")
	}
	col := index(rows[0])

	var out []record
	for _, row := range rows[1:] {
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}

		// Dup rows point at another entry and NonEx never existed; both would
		// only produce duplicate or phantom labels.
		switch get("Type") {
		case "Dup", "NonEx", "":
			continue
		}

		ra, ok1 := parseHMS(get("RA"))
		dec, ok2 := parseDMS(get("Dec"))
		if !ok1 || !ok2 {
			continue
		}

		name := formatDesignation(get("Name"))
		id := name
		var aliases []string

		// Messier numbers are how people actually refer to these objects, so
		// promote them to the primary label and demote the NGC/IC number.
		if m := strings.TrimLeft(get("M"), "0"); m != "" {
			id = "M " + m
			aliases = append(aliases, name)
		}

		mag, hasMag := firstFloat(get("V-Mag"), get("B-Mag"))

		out = append(out, record{
			Kind:    "d",
			ID:      id,
			Common:  firstCommonName(get("Common names")),
			Aliases: strings.Join(aliases, ","),
			RA:      ra,
			Dec:     dec,
			MajAx:   atof(get("MajAx")),
			MinAx:   atof(get("MinAx")),
			PA:      atof(get("PosAng")),
			Mag:     mag,
			HasMag:  hasMag,
		})
	}
	return out, nil
}

func index(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(h)] = i
	}
	return m
}

// firstCommonName keeps only the first of OpenNGC's comma-separated common
// names — "Andromeda Galaxy" rather than the full synonym list, which would
// never fit on an overlay label.
func firstCommonName(s string) string {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// parseHMS converts "HH:MM:SS.ss" to degrees.
func parseHMS(s string) (float64, bool) {
	h, m, sec, ok := split3(s)
	if !ok {
		return 0, false
	}
	return (h + m/60 + sec/3600) * 15, true
}

// parseDMS converts "+DD:MM:SS.s" to degrees.
func parseDMS(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	neg := strings.HasPrefix(s, "-")
	d, m, sec, ok := split3(strings.TrimLeft(s, "+-"))
	if !ok {
		return 0, false
	}
	v := d + m/60 + sec/3600
	if neg {
		v = -v
	}
	return v, true
}

func split3(s string) (a, b, c float64, ok bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var err error
	if a, err = strconv.ParseFloat(parts[0], 64); err != nil {
		return 0, 0, 0, false
	}
	if b, err = strconv.ParseFloat(parts[1], 64); err != nil {
		return 0, 0, 0, false
	}
	if c, err = strconv.ParseFloat(parts[2], 64); err != nil {
		return 0, 0, 0, false
	}
	return a, b, c, true
}

// --- VizieR tab-separated tables ---

// readVizieR yields the data rows of a VizieR asu-tsv response, skipping the
// comment preamble, the two header lines and the dashed separator.
func readVizieR(path string) ([][]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows [][]string
	seenSeparator := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// The dashed rule sits directly above the data.
		if strings.HasPrefix(line, "---") {
			seenSeparator = true
			continue
		}
		if !seenSeparator {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("no data rows in %s", path)
	}
	return rows, nil
}

func readSharpless(path string) ([]record, error) {
	rows, err := readVizieR(path)
	if err != nil {
		return nil, err
	}
	var out []record
	for _, r := range rows {
		if len(r) < 4 {
			continue
		}
		ra, dec, ok := coords(r[0], r[1])
		if !ok {
			continue
		}
		num := strings.TrimSpace(r[2])
		if num == "" {
			continue
		}
		// Sharpless records a single maximum diameter, so the glyph is a circle.
		d := atof(r[3])
		out = append(out, record{
			Kind: "e", ID: "Sh2-" + num,
			RA: ra, Dec: dec, MajAx: d, MinAx: d,
		})
	}
	return out, nil
}

// gumRef matches the "G12,13" style Gum cross-references in the RCW ID column.
var gumRef = regexp.MustCompile(`^G([\d,]+)$`)

func readRCW(path string) ([]record, error) {
	rows, err := readVizieR(path)
	if err != nil {
		return nil, err
	}
	var out []record
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		ra, dec, ok := coords(r[0], r[1])
		if !ok {
			continue
		}
		num := strings.TrimSpace(r[2])
		if num == "" {
			continue
		}
		maj, min := atof(r[3]), atof(r[4])
		if min == 0 {
			min = maj
		}

		// Gum's 1955 survey is not published as a machine-readable table
		// anywhere reachable, but RCW cross-references it, so Gum numbers come
		// along for free as aliases.
		var aliases []string
		if len(r) > 5 {
			for _, part := range strings.Split(r[5], ";") {
				if m := gumRef.FindStringSubmatch(strings.TrimSpace(part)); m != nil {
					for _, n := range strings.Split(m[1], ",") {
						if n = strings.TrimSpace(n); n != "" {
							aliases = append(aliases, "Gum "+n)
						}
					}
				}
			}
		}

		out = append(out, record{
			Kind: "e", ID: "RCW " + num, Aliases: strings.Join(aliases, ","),
			RA: ra, Dec: dec, MajAx: maj, MinAx: min,
		})
	}
	return out, nil
}

func readLBN(path string) ([]record, error) {
	rows, err := readVizieR(path)
	if err != nil {
		return nil, err
	}
	var out []record
	for _, r := range rows {
		if len(r) < 5 {
			continue
		}
		ra, dec, ok := coords(r[0], r[1])
		if !ok {
			continue
		}
		num := strings.TrimSpace(r[2])
		if num == "" {
			continue
		}
		maj, min := atof(r[3]), atof(r[4])
		if min == 0 {
			min = maj
		}
		var alias string
		if len(r) > 5 {
			alias = strings.TrimSpace(r[5])
		}
		out = append(out, record{
			Kind: "e", ID: "LBN " + num, Aliases: alias,
			RA: ra, Dec: dec, MajAx: maj, MinAx: min,
		})
	}
	return out, nil
}

func coords(raStr, decStr string) (ra, dec float64, ok bool) {
	ra, err1 := strconv.ParseFloat(strings.TrimSpace(raStr), 64)
	dec, err2 := strconv.ParseFloat(strings.TrimSpace(decStr), 64)
	return ra, dec, err1 == nil && err2 == nil
}

// --- HYG star database ---

// greek expands the three-letter Bayer abbreviations HYG uses.
var greek = map[string]string{
	"Alp": "α", "Bet": "β", "Gam": "γ", "Del": "δ", "Eps": "ε", "Zet": "ζ",
	"Eta": "η", "The": "θ", "Iot": "ι", "Kap": "κ", "Lam": "λ", "Mu": "μ",
	"Nu": "ν", "Xi": "ξ", "Omi": "ο", "Pi": "π", "Rho": "ρ", "Sig": "σ",
	"Tau": "τ", "Ups": "υ", "Phi": "φ", "Chi": "χ", "Psi": "ψ", "Ome": "ω",
}

// bayerPattern splits HYG's "21Alp And" into Flamsteed number, Bayer letter and
// constellation.
var bayerPattern = regexp.MustCompile(`^(\d*)([A-Za-z]+)(\d*)\s+(\w+)$`)

func formatBayer(bf string) string {
	m := bayerPattern.FindStringSubmatch(strings.TrimSpace(bf))
	if m == nil {
		return strings.TrimSpace(bf)
	}
	letter, ok := greek[m[2]]
	if !ok {
		return strings.TrimSpace(bf)
	}
	// A superscript index distinguishes e.g. π¹ from π² Gruis.
	return letter + m[3] + " " + m[4]
}

func readHYG(path string, magLimit float64) ([]record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cr := csv.NewReader(f)
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("no data rows")
	}
	col := index(rows[0])

	var out []record
	for _, row := range rows[1:] {
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		if get("id") == "0" { // the Sun
			continue
		}
		mag, err := strconv.ParseFloat(get("mag"), 64)
		if err != nil || mag > magLimit {
			continue
		}
		// HYG stores right ascension in hours.
		raHours, err1 := strconv.ParseFloat(get("ra"), 64)
		dec, err2 := strconv.ParseFloat(get("dec"), 64)
		if err1 != nil || err2 != nil {
			continue
		}

		// Prefer the name a person would actually use.
		var id string
		var aliases []string
		proper, bf := get("proper"), formatBayer(get("bf"))
		hd, hip := get("hd"), get("hip")
		switch {
		case proper != "":
			id = proper
			if bf != "" {
				aliases = append(aliases, bf)
			}
		case bf != "":
			id = bf
		case hd != "":
			id = "HD " + hd
		case hip != "":
			id = "HIP " + hip
		default:
			continue // nothing to label it with
		}
		if id != "HD "+hd && hd != "" {
			aliases = append(aliases, "HD "+hd)
		}

		out = append(out, record{
			Kind: "s", ID: id, Aliases: strings.Join(aliases, ","),
			RA: raHours * 15, Dec: dec,
			Mag: mag, HasMag: true,
		})
	}
	return out, nil
}

// --- curated extras ---

// extras covers designations the site actually uses that no bulk catalogue
// reachable from VizieR provides. Henize's 1956 survey of Magellanic Cloud
// emission objects is the case in point: it is only published under its LHA
// designations, so these two were resolved through SIMBAD by hand.
func extras() []record {
	return []record{
		{Kind: "e", ID: "Henize 206", Aliases: "LHA 120-N 206,N 206",
			RA: 82.9833, Dec: -71.0053, MajAx: 3.2, MinAx: 3.2},
		{Kind: "e", ID: "Henize 55", Aliases: "LHA 120-N 55,N 55",
			RA: 83.1371, Dec: -66.4056, MajAx: 7.0, MinAx: 5.0},
	}
}

// --- helpers ---

func atof(s string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return v
}

func firstFloat(vals ...string) (float64, bool) {
	for _, v := range vals {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}
