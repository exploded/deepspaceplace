package handlers

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
)

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

type emdashPoint struct {
	Month  string
	Count  int
	X      float64
	YLog   float64
	HLog   float64
	YLin   float64
	HLin   float64
	IsPeak bool
}

type emdashLinePoint struct {
	Month string
	Count int
	X     float64
	YLog  float64
	YLin  float64
}

type EmdashData struct {
	CanonicalURL  string
	Title         string
	Description   string
	Points        []emdashPoint
	LinePoints    []emdashLinePoint
	LinePathLog   string
	LinePathLin   string
	YTicksLog     []emdashTick
	YTicksLin     []emdashTick
	YTicksLogR    []emdashTick
	YTicksLinR    []emdashTick
	XLabels       []emdashXLabel
	ChartW       float64
	ChartH       float64
	PlotX        float64
	PlotY        float64
	PlotW        float64
	PlotH        float64
	BarW         float64
	Total        int
	Peak         emdashPoint
	Latest       emdashPoint
	AboutTotal   int
	AboutPeak    emdashLinePoint
	AboutLatest  emdashLinePoint
}

type emdashTick struct {
	Y     float64
	Label string
}

type emdashXLabel struct {
	X     float64
	Label string
}

var emdashRaw = []struct {
	Month string
	Count int
}{
	{"2021-01", 4334}, {"2021-02", 4535}, {"2021-03", 4300}, {"2021-04", 4664},
	{"2021-05", 3492}, {"2021-06", 4043}, {"2021-07", 3999}, {"2021-08", 4479},
	{"2021-09", 6633}, {"2021-10", 2576}, {"2021-11", 5715}, {"2021-12", 8445},
	{"2022-01", 7915}, {"2022-02", 7456}, {"2022-03", 8460}, {"2022-04", 6079},
	{"2022-05", 5862}, {"2022-06", 5151}, {"2022-07", 4757}, {"2022-08", 12780},
	{"2022-09", 52699}, {"2022-10", 7336}, {"2022-11", 5166}, {"2022-12", 4021},
	{"2023-01", 8691}, {"2023-02", 6963}, {"2023-03", 6828}, {"2023-04", 69336},
	{"2023-05", 18388}, {"2023-06", 3389}, {"2023-07", 5799}, {"2023-08", 3154},
	{"2023-09", 3249}, {"2023-10", 3346}, {"2023-11", 7323}, {"2023-12", 3650},
	{"2024-01", 3286}, {"2024-02", 2886}, {"2024-03", 21131}, {"2024-04", 3151},
	{"2024-05", 4251}, {"2024-06", 7947}, {"2024-07", 5159}, {"2024-08", 6212},
	{"2024-09", 5036}, {"2024-10", 6460}, {"2024-11", 7689}, {"2024-12", 9274},
	{"2025-01", 7672}, {"2025-02", 14450}, {"2025-03", 11191}, {"2025-04", 14501},
	{"2025-05", 29669}, {"2025-06", 25647}, {"2025-07", 101169}, {"2025-08", 430950},
	{"2025-09", 345853}, {"2025-10", 16751}, {"2025-11", 17310}, {"2025-12", 16074},
	{"2026-01", 18423}, {"2026-02", 84684}, {"2026-03", 476320}, {"2026-04", 546143},
}

// emdashAboutRaw: tighter query — issues that explicitly mention "em dash" or "em-dash"
// in title or body. These are issues *about* em-dashes (likely caused by them).
var emdashAboutRaw = []struct {
	Month string
	Count int
}{
	{"2021-01", 22}, {"2021-02", 39}, {"2021-03", 30}, {"2021-04", 25},
	{"2021-05", 19}, {"2021-06", 23}, {"2021-07", 21}, {"2021-08", 17},
	{"2021-09", 21}, {"2021-10", 18}, {"2021-11", 30}, {"2021-12", 14},
	{"2022-01", 34}, {"2022-02", 22}, {"2022-03", 23}, {"2022-04", 24},
	{"2022-05", 39}, {"2022-06", 23}, {"2022-07", 86}, {"2022-08", 45},
	{"2022-09", 38}, {"2022-10", 38}, {"2022-11", 39}, {"2022-12", 37},
	{"2023-01", 27}, {"2023-02", 25}, {"2023-03", 24}, {"2023-04", 33},
	{"2023-05", 27}, {"2023-06", 23}, {"2023-07", 45}, {"2023-08", 43},
	{"2023-09", 25}, {"2023-10", 40}, {"2023-11", 25}, {"2023-12", 29},
	{"2024-01", 27}, {"2024-02", 49}, {"2024-03", 36}, {"2024-04", 24},
	{"2024-05", 34}, {"2024-06", 30}, {"2024-07", 25}, {"2024-08", 37},
	{"2024-09", 23}, {"2024-10", 40}, {"2024-11", 43}, {"2024-12", 45},
	{"2025-01", 68}, {"2025-02", 50}, {"2025-03", 50}, {"2025-04", 62},
	{"2025-05", 45}, {"2025-06", 58}, {"2025-07", 34}, {"2025-08", 48},
	{"2025-09", 141}, {"2025-10", 82}, {"2025-11", 57}, {"2025-12", 42},
	{"2026-01", 60}, {"2026-02", 78}, {"2026-03", 94}, {"2026-04", 53},
}

func HandleEmdash(w http.ResponseWriter, r *http.Request) {
	const (
		chartW = 960.0
		chartH = 460.0
		padL   = 60.0
		padR   = 60.0
		padT   = 30.0
		padB   = 60.0
	)
	plotW := chartW - padL - padR
	plotH := chartH - padT - padB

	n := len(emdashRaw)
	barW := plotW / float64(n)

	// Log scale: 10 to 1,000,000
	const yMinLog, yMaxLog = 10.0, 1000000.0
	logMin := math.Log10(yMinLog)
	logMax := math.Log10(yMaxLog)
	scaleLog := func(v float64) float64 {
		if v < yMinLog {
			v = yMinLog
		}
		t := (math.Log10(v) - logMin) / (logMax - logMin)
		return padT + plotH*(1-t)
	}

	// Linear scale: 0 to 600,000 (covers the 546K peak)
	const yMaxLin = 600000.0
	scaleLin := func(v float64) float64 {
		t := v / yMaxLin
		if t > 1 {
			t = 1
		}
		return padT + plotH*(1-t)
	}

	// Right-axis scales for the "about" series (peaks at 141)
	const yMinLogR, yMaxLogR = 1.0, 1000.0
	logMinR := math.Log10(yMinLogR)
	logMaxR := math.Log10(yMaxLogR)
	scaleLogR := func(v float64) float64 {
		if v < yMinLogR {
			v = yMinLogR
		}
		t := (math.Log10(v) - logMinR) / (logMaxR - logMinR)
		return padT + plotH*(1-t)
	}
	const yMaxLinR = 150.0
	scaleLinR := func(v float64) float64 {
		t := v / yMaxLinR
		if t > 1 {
			t = 1
		}
		return padT + plotH*(1-t)
	}

	plotBottom := padT + plotH

	points := make([]emdashPoint, n)
	total := 0
	peakIdx := 0
	for i, r := range emdashRaw {
		x := padL + float64(i)*barW
		yL := scaleLog(float64(r.Count))
		yN := scaleLin(float64(r.Count))
		points[i] = emdashPoint{
			Month: r.Month,
			Count: r.Count,
			X:     x,
			YLog:  yL,
			HLog:  plotBottom - yL,
			YLin:  yN,
			HLin:  plotBottom - yN,
		}
		total += r.Count
		if r.Count > emdashRaw[peakIdx].Count {
			peakIdx = i
		}
	}
	points[peakIdx].IsPeak = true

	// "About em-dash" series — line points and SVG paths for both scales
	linePoints := make([]emdashLinePoint, n)
	aboutTotal := 0
	aboutPeakIdx := 0
	pathLog := ""
	pathLin := ""
	for i, r := range emdashAboutRaw {
		x := padL + float64(i)*barW + barW/2
		yL := scaleLogR(float64(r.Count))
		yN := scaleLinR(float64(r.Count))
		linePoints[i] = emdashLinePoint{
			Month: r.Month,
			Count: r.Count,
			X:     x,
			YLog:  yL,
			YLin:  yN,
		}
		aboutTotal += r.Count
		if r.Count > emdashAboutRaw[aboutPeakIdx].Count {
			aboutPeakIdx = i
		}
		sep := " L"
		if i == 0 {
			sep = "M"
		}
		pathLog += sep + fmtFloat(x) + "," + fmtFloat(yL)
		pathLin += sep + fmtFloat(x) + "," + fmtFloat(yN)
	}

	yTicksLog := []emdashTick{}
	for exp := 1; exp <= 6; exp++ {
		v := math.Pow10(exp)
		yTicksLog = append(yTicksLog, emdashTick{
			Y:     scaleLog(v),
			Label: logTickLabel(v),
		})
	}

	yTicksLin := []emdashTick{}
	for v := 0.0; v <= yMaxLin; v += 100000 {
		yTicksLin = append(yTicksLin, emdashTick{
			Y:     scaleLin(v),
			Label: linTickLabel(v),
		})
	}

	yTicksLogR := []emdashTick{}
	for exp := 0; exp <= 3; exp++ {
		v := math.Pow10(exp)
		yTicksLogR = append(yTicksLogR, emdashTick{
			Y:     scaleLogR(v),
			Label: logTickLabel(v),
		})
	}

	yTicksLinR := []emdashTick{}
	for v := 0.0; v <= yMaxLinR; v += 30 {
		yTicksLinR = append(yTicksLinR, emdashTick{
			Y:     scaleLinR(v),
			Label: strconv.Itoa(int(v)),
		})
	}

	xLabels := []emdashXLabel{}
	for i, r := range emdashRaw {
		if r.Month[5:] == "01" || (i == 0) {
			xLabels = append(xLabels, emdashXLabel{
				X:     padL + float64(i)*barW + barW/2,
				Label: r.Month[:4],
			})
		}
	}

	data := EmdashData{
		CanonicalURL: "https://deepspaceplace.com/emdash",
		Title:        "The Rise of the Em-Dash in GitHub Issues",
		Description:  "Tracking the explosive growth of em-dash usage in GitHub issues from 2021 to 2026 — a fingerprint of LLM-generated text.",
		Points:       points,
		LinePoints:   linePoints,
		LinePathLog:  pathLog,
		LinePathLin:  pathLin,
		YTicksLog:    yTicksLog,
		YTicksLin:    yTicksLin,
		YTicksLogR:   yTicksLogR,
		YTicksLinR:   yTicksLinR,
		XLabels:      xLabels,
		ChartW:       chartW,
		ChartH:       chartH,
		PlotX:        padL,
		PlotY:        padT,
		PlotW:        plotW,
		PlotH:        plotH,
		BarW:         barW,
		Total:        total,
		Peak:         points[peakIdx],
		Latest:       points[n-1],
		AboutTotal:   aboutTotal,
		AboutPeak:    linePoints[aboutPeakIdx],
		AboutLatest:  linePoints[n-1],
	}

	Render(w, "emdash.html", data)
}

func logTickLabel(v float64) string {
	switch v {
	case 1:
		return "1"
	case 10:
		return "10"
	case 100:
		return "100"
	case 1000:
		return "1K"
	case 10000:
		return "10K"
	case 100000:
		return "100K"
	case 1000000:
		return "1M"
	}
	return ""
}

func linTickLabel(v float64) string {
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%dK", int(v/1000))
}
