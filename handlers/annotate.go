package handlers

import (
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"deepspaceplace/internal/catalog"
	"deepspaceplace/internal/database"
	"deepspaceplace/internal/wcs"
)

// Overlay is everything needed to draw the annotation layer as inline SVG.
//
// All geometry is in the image's own pixel coordinates and the template emits
// a viewBox of W by H, so the layer stretches to whatever size the browser
// renders the photograph at. That also means it survives the admin resizer,
// which rewrites images in place at up to 3840px on the long edge.
type Overlay struct {
	W, H int

	// Font sizes are in viewBox units, so they must scale with the image for a
	// 4000px frame and a 1500px frame to look the same on screen. Stroke widths
	// need no equivalent: the stylesheet gives them non-scaling-stroke, which
	// measures in screen pixels.
	FontSize  float64
	SmallFont float64

	Objects    []OverlayObject
	GridLines  []GridLine
	GridLabels []GridLabel
	ScaleBar   *ScaleBar
	Compass    *Compass
	Credit     string
}

// OverlayObject is one catalogue object drawn over the image.
type OverlayObject struct {
	Class string // "dso", "emission" or "star", used for the layer toggles
	X, Y  float64

	// Ellipse geometry. Point is true for objects with no cataloged size, and
	// for stars, which get a small fixed marker instead.
	RX, RY, Angle float64
	Point         bool
	PointR        float64

	// Label is the object's usual name and Sub the catalogue designation when
	// the two differ. Both are empty when the label was dropped to avoid
	// overlapping one already placed.
	Label          string
	Sub            string
	LabelX, LabelY float64
	SubY           float64 // baseline of the second line
	LabelAnchor    string  // SVG text-anchor
}

// GridLine is one segment of a constant right ascension or declination line,
// pre-rendered as an SVG points list.
type GridLine struct {
	Points string
}

// GridLabel annotates a grid line with its coordinate.
type GridLabel struct {
	X, Y float64
	Text string
}

// ScaleBar shows how much sky a span of the image covers.
type ScaleBar struct {
	Path         string // the bar and its end ticks
	TextX, TextY float64
	Text         string
}

// Compass shows which way north and east lie, which is the fastest way to see
// at a glance that the overlay is registered correctly.
type Compass struct {
	Path                     string // an arm to north and an arm to east
	NorthLabelX, NorthLabelY float64
	EastLabelX, EastLabelY   float64
}

// Label caps per layer. Deep-sky fields near the galactic plane can hold
// hundreds of catalogued objects; past a few dozen the labels stop being
// information and start being a wall of text.
const (
	maxDSO      = 60
	maxEmission = 40
	maxStars    = 40
)

// aspectTolerance is how far the solved field's shape may differ from the
// actual image's shape before the solve is treated as untrustworthy. Several
// rows in this database predate the automated solver and carry hand-typed
// field sizes; where those disagree with the image, an overlay would be
// confidently wrong, which is worse than no overlay.
const aspectTolerance = 0.03

// BuildOverlay assembles the annotation layer for an image, or returns nil if
// the image has no usable plate solve.
func BuildOverlay(img database.Image) *Overlay {
	if !img.Ra.Valid || !img.Dec.Valid || !img.Pixscale.Valid ||
		!img.WidthArcsec.Valid || !img.HeightArcsec.Valid {
		return nil
	}
	if img.Solved == "f" {
		return nil
	}

	// The stored pixel scale describes the image as it was solved, but the
	// file on disk may since have been downscaled. Measuring the file and
	// deriving the scale from the angular width keeps the two in step.
	w, h, ok := imageDims(img.Filename)
	if !ok {
		return nil
	}
	widthArcsec, heightArcsec := img.WidthArcsec.Float64, img.HeightArcsec.Float64
	solvedAspect := widthArcsec / heightArcsec
	actualAspect := float64(w) / float64(h)
	if math.Abs(solvedAspect-actualAspect)/actualAspect > aspectTolerance {
		slog.Info("Skipping annotation overlay: solved field does not match the image",
			"id", img.ID, "solved_aspect", solvedAspect, "image_aspect", actualAspect)
		return nil
	}
	pixScale := widthArcsec / float64(w)

	// Parity is NULL for every row solved before it was captured. Normal is
	// the right default: every image checked while calibrating this was
	// unmirrored.
	parity := 1.0
	if img.Parity.Valid {
		parity = img.Parity.Float64
	}
	orientation := 0.0
	if img.Orientation.Valid {
		orientation = img.Orientation.Float64
	}

	sky, ok := wcs.FromAstrometry(img.Ra.Float64, img.Dec.Float64, pixScale, orientation, parity, w, h)
	if !ok {
		return nil
	}

	diag := math.Hypot(float64(w), float64(h))
	unit := diag / 1000

	o := &Overlay{
		W: w, H: h,
		FontSize:  round1(13 * unit),
		SmallFont: round1(10 * unit),
		Credit:    "OpenNGC · Sharpless · RCW · LBN · HYG",
	}
	o.Objects = buildObjects(sky, unit)
	o.GridLines, o.GridLabels = buildGrid(sky, unit)
	o.ScaleBar = buildScaleBar(sky, unit)
	o.Compass = buildCompass(sky, unit)
	return o
}

// ImagesDir is where the photographs live, relative to the working directory,
// matching how the rest of the handlers reach them. It is a variable so tests
// can point it at a fixture directory.
var ImagesDir = "images"

// imageDims reads the pixel dimensions from an image file's header without
// decoding it.
func imageDims(filename string) (w, h int, ok bool) {
	if filename == "" || strings.ContainsAny(filename, `/\`) {
		return 0, 0, false
	}
	f, err := os.Open(filepath.Join(ImagesDir, filename))
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// --- objects ---

// candidate is a catalogue object once it has been placed on the image, which
// is what ranking needs to know about.
type candidate struct {
	obj     catalog.Object
	x, y    float64
	rx, ry  float64
	inFrame bool
}

func buildObjects(sky wcs.WCS, unit float64) []OverlayObject {
	found := catalog.Query(sky.RA0, sky.Dec0, sky.RadiusDeg())

	const marginFactor = 0.02
	diagonal := math.Hypot(float64(sky.W), float64(sky.H))
	margin := marginFactor * diagonal
	minR := 4 * unit

	// Project first, then rank: whether an object actually lands on the picture
	// is the most important thing about it, and that is not knowable from the
	// catalogue alone.
	byKind := map[catalog.Kind][]candidate{}
	for _, obj := range found {
		x, y, ok := sky.Project(obj.RA, obj.Dec)
		if !ok {
			continue
		}

		// Catalogue sizes are full diameters in arcminutes.
		rx := obj.MajAx * 60 / sky.PixScale / 2
		ry := obj.MinAx * 60 / sky.PixScale / 2
		if ry <= 0 {
			ry = rx
		}

		// An object whose outline reaches the frame is worth drawing even when
		// its centre lies well outside it -- that is the usual case for the
		// large southern emission complexes, which are degrees across.
		if !sky.Contains(x, y, margin+rx) {
			continue
		}

		// Past a certain size the outline is indistinguishable from a straight
		// line and says only "this frame is somewhere inside it", which is not
		// worth a stray stroke across the picture.
		if rx > 8*diagonal {
			continue
		}

		byKind[obj.Kind] = append(byKind[obj.Kind], candidate{
			obj: obj, x: x, y: y, rx: rx, ry: ry,
			inFrame: sky.Contains(x, y, margin),
		})
	}

	// Rank so that capping keeps whatever is most worth seeing: objects
	// centred on the picture first, then the largest, and for stars the
	// brightest.
	bySize := func(list []candidate) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].inFrame != list[j].inFrame {
				return list[i].inFrame
			}
			return list[i].obj.MajAx > list[j].obj.MajAx
		})
	}
	bySize(byKind[catalog.DSO])
	bySize(byKind[catalog.Emission])
	sort.SliceStable(byKind[catalog.Star], func(i, j int) bool {
		s := byKind[catalog.Star]
		if s[i].inFrame != s[j].inFrame {
			return s[i].inFrame
		}
		return s[i].obj.Mag < s[j].obj.Mag
	})

	var placed []labelBox
	var out []OverlayObject

	layers := []struct {
		kind  catalog.Kind
		class string
		limit int
	}{
		{catalog.DSO, "dso", maxDSO},
		{catalog.Emission, "emission", maxEmission},
		{catalog.Star, "star", maxStars},
	}

	for _, layer := range layers {
		n := 0
		for _, c := range byKind[layer.kind] {
			if n >= layer.limit {
				break
			}
			n++

			obj, x, y, rx, ry := c.obj, c.x, c.y, c.rx, c.ry
			ov := OverlayObject{Class: layer.class, X: round1(x), Y: round1(y)}

			if layer.kind == catalog.Star || rx < minR {
				ov.Point = true
				ov.PointR = round1(minR)
			} else {
				ov.RX, ov.RY = round1(rx), round1(ry)
				ov.Angle = round1(ellipseAngle(sky, obj.PA))
			}

			label := obj.Label()
			sub := ""
			if obj.Common != "" && obj.ID != obj.Common {
				sub = obj.ID
			}

			// Offset the label clear of the glyph.
			gap := 3 * unit
			offset := ov.PointR + gap
			if !ov.Point {
				offset = math.Min(rx, 40*unit) + gap
			}

			// A nebula bigger than the frame has its centre off the picture, so
			// anchoring the label there would push it outside and lose it.
			// Draw it in from the edge on the side the object lies, which is
			// where its arc crosses the frame anyway.
			inset := 10 * unit
			ax := clamp(x, inset, float64(sky.W)-inset)
			ay := clamp(y, inset, float64(sky.H)-inset)
			if ax != x || ay != y {
				offset = gap
			}

			if lx, ly, anchor, ok := placeLabel(sky, &placed, ax, ay, offset, label, sub, unit); ok {
				ov.Label, ov.Sub = label, sub
				ov.LabelX, ov.LabelY, ov.LabelAnchor = round1(lx), round1(ly), anchor
				ov.SubY = round1(ly + 13*unit*1.05)
			}
			out = append(out, ov)
		}
	}
	return out
}

// skyDirection gives the unit vector, in image coordinates, of a step away from
// the field centre along a position angle measured east of north.
//
// This is the same rotation the projection applies, written out for a
// direction rather than a point, so the compass and the ellipse orientations
// cannot drift out of agreement with where objects actually land.
func skyDirection(sky wcs.WCS, paDeg float64) (dx, dy float64) {
	sinPA, cosPA := math.Sincos(paDeg * math.Pi / 180)
	sinT, cosT := math.Sincos(sky.Orientation * math.Pi / 180)
	p := float64(sky.Parity)

	dx = p * (sinPA*cosT - cosPA*sinT)
	dy = -(sinPA*sinT + cosPA*cosT)
	return dx, dy
}

// ellipseAngle converts a catalogue position angle, measured east of north on
// the sky, into the rotation an SVG ellipse needs so its major axis lies along
// that direction in the image.
func ellipseAngle(sky wcs.WCS, paDeg float64) float64 {
	dx, dy := skyDirection(sky, paDeg)
	return math.Atan2(dy, dx) * 180 / math.Pi
}

// labelBox is a placed label's bounding box, used to keep labels off each other.
type labelBox struct{ x0, y0, x1, y1 float64 }

func (b labelBox) overlaps(o labelBox) bool {
	return b.x0 < o.x1 && o.x0 < b.x1 && b.y0 < o.y1 && o.y0 < b.y1
}

// placeLabel finds a free spot for a label near a glyph, trying each side in
// turn. It reports false when every candidate collides, in which case the
// object keeps its glyph but loses its text.
func placeLabel(sky wcs.WCS, placed *[]labelBox, x, y, offset float64, label, sub string, unit float64) (lx, ly float64, anchor string, ok bool) {
	fontSize := 13 * unit
	lines := 1.0
	if sub != "" {
		lines = 2
	}
	// 0.55em per character is a serviceable estimate of average glyph advance
	// for the sans-serif stack this renders in.
	width := float64(maxLen(label, sub)) * fontSize * 0.55
	height := lines * fontSize * 1.15

	candidates := []struct {
		dx, dy float64
		anchor string
	}{
		{offset, -offset, "start"},
		{-offset, -offset, "end"},
		{offset, offset + height, "start"},
		{-offset, offset + height, "end"},
	}

	for _, c := range candidates {
		lx, ly := x+c.dx, y+c.dy
		box := labelBox{x0: lx, y0: ly - fontSize, x1: lx + width, y1: ly + height - fontSize}
		if c.anchor == "end" {
			box.x0, box.x1 = lx-width, lx
		}
		// Keep labels inside the frame, or they get clipped away.
		if box.x0 < 0 || box.y0 < 0 || box.x1 > float64(sky.W) || box.y1 > float64(sky.H) {
			continue
		}
		clash := false
		for _, p := range *placed {
			if box.overlaps(p) {
				clash = true
				break
			}
		}
		if clash {
			continue
		}
		*placed = append(*placed, box)
		return lx, ly, c.anchor, true
	}
	return 0, 0, "", false
}

func maxLen(a, b string) int {
	if len([]rune(b)) > len([]rune(a)) {
		return len([]rune(b))
	}
	return len([]rune(a))
}

// --- coordinate grid ---

// gridSteps are the angular spacings the grid is allowed to use, in arcseconds,
// running from one arcsecond to ten degrees. Restricting to round numbers is
// what makes the labels readable.
var gridSteps = []float64{
	1, 2, 5, 10, 15, 30,
	60, 120, 300, 600, 900, 1800,
	3600, 7200, 18000, 36000,
}

func buildGrid(sky wcs.WCS, unit float64) ([]GridLine, []GridLabel) {
	minDec, maxDec, minRAOff, maxRAOff := frameExtent(sky)

	decStep := chooseStep((maxDec - minDec) * 3600)
	// Right ascension lines crowd together as declination increases, so space
	// them by the angle they actually subtend on the sky.
	cosDec := math.Cos(sky.Dec0 * math.Pi / 180)
	if cosDec < 0.02 {
		cosDec = 0.02
	}
	raStep := chooseStep((maxRAOff - minRAOff) * 3600 * cosDec)

	var lines []GridLine
	var labels []GridLabel

	addLine := func(pts []point, text string) {
		for _, seg := range splitVisible(sky, pts) {
			lines = append(lines, GridLine{Points: pointsString(seg)})
			if text != "" {
				mid := seg[len(seg)/2]
				labels = append(labels, GridLabel{X: round1(mid.x), Y: round1(mid.y), Text: text})
				text = "" // one label per line, not per segment
			}
		}
	}

	const samples = 96

	// Lines of constant declination.
	for dec := math.Ceil(minDec*3600/decStep) * decStep / 3600; dec <= maxDec; dec += decStep / 3600 {
		if dec < -90 || dec > 90 {
			continue
		}
		pts := make([]point, 0, samples+1)
		for i := 0; i <= samples; i++ {
			raOff := minRAOff + (maxRAOff-minRAOff)*float64(i)/samples
			x, y, ok := sky.Project(sky.RA0+raOff, dec)
			pts = append(pts, point{x, y, ok})
		}
		addLine(pts, formatDec(dec, decStep))
	}

	// Lines of constant right ascension.
	for raOff := math.Ceil(minRAOff*3600/raStep) * raStep / 3600; raOff <= maxRAOff; raOff += raStep / 3600 {
		pts := make([]point, 0, samples+1)
		for i := 0; i <= samples; i++ {
			dec := minDec + (maxDec-minDec)*float64(i)/samples
			x, y, ok := sky.Project(sky.RA0+raOff, dec)
			pts = append(pts, point{x, y, ok})
		}
		addLine(pts, formatRA(sky.RA0+raOff, raStep))
	}

	return lines, labels
}

type point struct {
	x, y float64
	ok   bool
}

// frameExtent finds the range of declination and of right ascension offset from
// the field centre that the frame covers, by walking its border. Right
// ascension is tracked as a signed offset so that a frame straddling zero hours
// does not produce a range of nearly 360 degrees.
func frameExtent(sky wcs.WCS) (minDec, maxDec, minRAOff, maxRAOff float64) {
	minDec, maxDec = 90, -90
	minRAOff, maxRAOff = 180, -180

	w, h := float64(sky.W), float64(sky.H)
	const steps = 32
	var samples [][2]float64
	for i := 0; i <= steps; i++ {
		f := float64(i) / steps
		samples = append(samples,
			[2]float64{f * w, 0}, [2]float64{f * w, h},
			[2]float64{0, f * h}, [2]float64{w, f * h},
		)
	}
	samples = append(samples, [2]float64{w / 2, h / 2})

	for _, s := range samples {
		ra, dec := sky.Deproject(s[0], s[1])
		minDec = math.Min(minDec, dec)
		maxDec = math.Max(maxDec, dec)
		off := wrap180(ra - sky.RA0)
		minRAOff = math.Min(minRAOff, off)
		maxRAOff = math.Max(maxRAOff, off)
	}

	// A frame containing a celestial pole wraps through every hour of right
	// ascension; sampling the border alone would miss that.
	if maxDec >= 89.999 || minDec <= -89.999 {
		minRAOff, maxRAOff = -180, 180
	}
	return minDec, maxDec, minRAOff, maxRAOff
}

// splitVisible breaks a projected path into the runs that are actually on the
// image, so a line that leaves and re-enters does not get a chord drawn across
// the frame.
func splitVisible(sky wcs.WCS, pts []point) [][]point {
	var out [][]point
	var cur []point
	const margin = 2
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, cur)
		}
		cur = nil
	}
	for _, p := range pts {
		if p.ok && sky.Contains(p.x, p.y, margin) {
			cur = append(cur, p)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func pointsString(pts []point) string {
	var b strings.Builder
	for i, p := range pts {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%.1f,%.1f", p.x, p.y)
	}
	return b.String()
}

// chooseStep picks the largest round spacing that still yields several lines
// across the given span.
func chooseStep(spanArcsec float64) float64 {
	const want = 6
	for _, s := range gridSteps {
		if spanArcsec/s <= want {
			return s
		}
	}
	return gridSteps[len(gridSteps)-1]
}

func wrap180(deg float64) float64 {
	for deg > 180 {
		deg -= 360
	}
	for deg < -180 {
		deg += 360
	}
	return deg
}

// formatDec renders a declination, dropping the components the grid spacing
// makes meaningless.
func formatDec(dec, stepArcsec float64) string {
	sign := "+"
	v := dec
	if v < 0 {
		sign, v = "−", -v // proper minus sign
	}
	// Round to the step so floating point drift does not show up as 41' 59".
	total := math.Round(v*3600/stepArcsec) * stepArcsec
	d := int(total / 3600)
	m := int(math.Mod(total, 3600) / 60)
	s := math.Mod(total, 60)

	switch {
	case stepArcsec >= 3600:
		return fmt.Sprintf("%s%d°", sign, d)
	case stepArcsec >= 60:
		return fmt.Sprintf("%s%d° %02d′", sign, d, m)
	default:
		return fmt.Sprintf("%s%d° %02d′ %02.0f″", sign, d, m, s)
	}
}

// formatRA renders a right ascension in time units, which is how they are
// labelled on every star chart.
func formatRA(ra, stepArcsec float64) string {
	ra = math.Mod(ra, 360)
	if ra < 0 {
		ra += 360
	}
	stepSec := stepArcsec / 15 // arcseconds to seconds of time
	totalSec := math.Round(ra*240/stepSec) * stepSec
	totalSec = math.Mod(totalSec, 86400)

	h := int(totalSec / 3600)
	m := int(math.Mod(totalSec, 3600) / 60)
	s := math.Mod(totalSec, 60)

	switch {
	case stepSec >= 3600:
		return fmt.Sprintf("%dh", h)
	case stepSec >= 60:
		return fmt.Sprintf("%dh %02dm", h, m)
	default:
		return fmt.Sprintf("%dh %02dm %02.0fs", h, m, s)
	}
}

// --- scale bar and compass ---

func buildScaleBar(sky wcs.WCS, unit float64) *ScaleBar {
	// Aim for a bar about a fifth of the frame, rounded to a sensible angle.
	target := float64(sky.W) * 0.2 * sky.PixScale
	step := gridSteps[0]
	for _, s := range gridSteps {
		if s <= target {
			step = s
		}
	}
	length := step / sky.PixScale
	if length < 10*unit {
		return nil
	}

	inset := 30 * unit
	tick := 7 * unit
	x := inset
	y := float64(sky.H) - inset

	return &ScaleBar{
		Path: fmt.Sprintf("M%.1f,%.1f L%.1f,%.1f L%.1f,%.1f L%.1f,%.1f",
			x, y-tick, x, y, x+length, y, x+length, y-tick),
		TextX: round1(x),
		TextY: round1(y - tick - 4*unit),
		Text:  formatAngle(step),
	}
}

func formatAngle(arcsec float64) string {
	switch {
	case arcsec >= 3600:
		return fmt.Sprintf("%g°", arcsec/3600)
	case arcsec >= 60:
		return fmt.Sprintf("%g′", arcsec/60)
	default:
		return fmt.Sprintf("%g″", arcsec)
	}
}

func buildCompass(sky wcs.WCS, unit float64) *Compass {
	inset := 45 * unit
	cx := float64(sky.W) - inset
	cy := float64(sky.H) - inset
	arm := 26 * unit

	nx, ny := skyDirection(sky, 0)
	ex, ey := skyDirection(sky, 90)

	return &Compass{
		Path: fmt.Sprintf("M%.1f,%.1f L%.1f,%.1f L%.1f,%.1f",
			cx+nx*arm, cy+ny*arm, cx, cy, cx+ex*arm, cy+ey*arm),
		NorthLabelX: round1(cx + nx*arm*1.35), NorthLabelY: round1(cy + ny*arm*1.35),
		EastLabelX: round1(cx + ex*arm*1.35), EastLabelY: round1(cy + ey*arm*1.35),
	}
}

func clamp(v, lo, hi float64) float64 {
	if hi < lo {
		return lo
	}
	return math.Min(math.Max(v, lo), hi)
}

// round1 keeps the emitted SVG compact: without it, template formatting of a
// float64 produces seventeen significant digits per coordinate.
func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
