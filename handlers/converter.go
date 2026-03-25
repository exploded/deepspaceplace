package handlers

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
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
					raHours := ra / 15.0
					h := int(raHours)
					minDec := (raHours - float64(h)) * 60
					m := int(minDec)
					s := (minDec - float64(m)) * 60
					data.ResultHMS = fmt.Sprintf("RA: %02dh %02dm %06.3fs", h, m, s)
				}
			}
			if data.DecDecimal != "" {
				dec, err := strconv.ParseFloat(data.DecDecimal, 64)
				if err == nil {
					sign := "+"
					if dec < 0 {
						sign = "-"
						dec = math.Abs(dec)
					}
					d := int(dec)
					minDec := (dec - float64(d)) * 60
					m := int(minDec)
					s := (minDec - float64(m)) * 60
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

	Render(w, "converter.html", ConverterData{})
}

func parseHMS(input string) float64 {
	re := regexp.MustCompile(`[hms°'"]+`)
	clean := re.ReplaceAllString(input, " ")
	clean = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(clean, " "))
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

	re := regexp.MustCompile(`[°'"hms+\-]+`)
	clean := re.ReplaceAllString(input, " ")
	clean = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(clean, " "))
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
