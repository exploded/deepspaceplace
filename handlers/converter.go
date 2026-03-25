package handlers

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	reHMSSymbols = regexp.MustCompile(`[hms°'"]+`)
	reDMSSymbols = regexp.MustCompile(`[°'"hms+\-]+`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

type ConverterData struct {
	RADecimal  string
	DecDecimal string
	RAHMS      string
	DecDMS     string
	ResultHMS  string
	ResultDMS  string
	ResultRA   string
	ResultDec  string
	ShowHMS    bool
	ShowDec    bool
}

func HandleConverter(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if r.Method == http.MethodPost {
		r.ParseForm()
		data := ConverterData{}

		if r.FormValue("convert_to_hms") != "" {
			data.RADecimal = strings.TrimSpace(r.FormValue("ra_decimal"))
			data.DecDecimal = strings.TrimSpace(r.FormValue("dec_decimal"))
			data.ShowHMS = true

			if data.RADecimal != "" {
				ra, err := strconv.ParseFloat(data.RADecimal, 64)
				if err == nil {
					h, m, s := splitHMS(ra)
					data.ResultHMS = fmt.Sprintf("RA: %02dh %02dm %06.3fs", h, m, s)
				}
			}
			if data.DecDecimal != "" {
				dec, err := strconv.ParseFloat(data.DecDecimal, 64)
				if err == nil {
					sign, d, m, s := splitDMS(dec)
					data.ResultDMS = fmt.Sprintf("DEC: %s%02d° %02d' %06.3f\"", sign, d, m, s)
				}
			}

			// Check if HTMX request
			if r.Header.Get("HX-Request") == "true" {
				RenderPartial(w, "converter.html", "converter_result.html", data)
				return
			}
		}

		if r.FormValue("convert_to_decimal") != "" {
			data.RAHMS = strings.TrimSpace(r.FormValue("ra_hms"))
			data.DecDMS = strings.TrimSpace(r.FormValue("dec_dms"))
			data.ShowDec = true

			if data.RAHMS != "" {
				ra := parseHMS(data.RAHMS)
				if ra >= 0 {
					data.ResultRA = fmt.Sprintf("RA: %.6f°", ra*15.0)
				}
			}
			if data.DecDMS != "" {
				dec, ok := parseDMS(data.DecDMS)
				if ok {
					data.ResultDec = fmt.Sprintf("DEC: %.6f°", dec)
				}
			}

			if r.Header.Get("HX-Request") == "true" {
				RenderPartial(w, "converter.html", "converter_result.html", data)
				return
			}
		}

		Render(w, "converter.html", data)
		return
	}

	// Pre-fill with example values
	h, m, s := splitHMS(115.624)
	sign, d, dm, ds := splitDMS(-14.819)
	raDecResult := parseHMS("07 41 46.0")
	decResult, _ := parseDMS("-14 30 00")
	Render(w, "converter.html", ConverterData{
		RADecimal:  "115.624",
		DecDecimal: "-14.819",
		ShowHMS:    true,
		ResultHMS:  fmt.Sprintf("RA: %02dh %02dm %06.3fs", h, m, s),
		ResultDMS:  fmt.Sprintf("DEC: %s%02d° %02d' %06.3f\"", sign, d, dm, ds),
		RAHMS:      "07h 41m 46.0s",
		DecDMS:     "-14° 30' 00\"",
		ShowDec:    true,
		ResultRA:   fmt.Sprintf("RA: %.6f°", raDecResult*15.0),
		ResultDec:  fmt.Sprintf("DEC: %.6f°", decResult),
	})
}

func parseHMS(input string) float64 {
	clean := reHMSSymbols.ReplaceAllString(input, " ")
	clean = strings.TrimSpace(reWhitespace.ReplaceAllString(clean, " "))
	parts := strings.Split(clean, " ")

	if len(parts) < 1 {
		return -1
	}

	h, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return -1
	}
	var m, s float64
	if len(parts) > 1 {
		m, _ = strconv.ParseFloat(parts[1], 64)
	}
	if len(parts) > 2 {
		s, _ = strconv.ParseFloat(parts[2], 64)
	}

	return h + m/60.0 + s/3600.0
}

func parseDMS(input string) (float64, bool) {
	negative := strings.Contains(input, "-")

	clean := reDMSSymbols.ReplaceAllString(input, " ")
	clean = strings.TrimSpace(reWhitespace.ReplaceAllString(clean, " "))
	parts := strings.Split(clean, " ")

	if len(parts) < 1 {
		return 0, false
	}

	d, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, false
	}
	var m, s float64
	if len(parts) > 1 {
		m, _ = strconv.ParseFloat(parts[1], 64)
	}
	if len(parts) > 2 {
		s, _ = strconv.ParseFloat(parts[2], 64)
	}

	result := math.Abs(d) + m/60.0 + s/3600.0
	if negative {
		result = -result
	}
	return result, true
}
