// Package numfmt formats counts for display. Compact is the one shared formatter for large numbers.
package numfmt

import (
	"math"
	"strconv"
	"strings"
)

// units are the magnitude suffixes, one per factor of 1000: thousand, million, billion, trillion,
// quadrillion, quintillion (int64 never goes beyond that).
var units = []string{"K", "M", "B", "T", "Qa", "Qi"}

// Compact abbreviates n by magnitude: plain below 1000, then K, M, B, T, Qa, Qi with one decimal
// below 10 of a unit and none from 10 (1.2K, 12K, 123K, 1.2M). A trailing ".0" is dropped and a value
// that rounds up to 1000 moves to the next unit (999999 is 1M). Negative numbers keep their sign.
func Compact(n int64) string {
	if n < 0 {
		if n == math.MinInt64 {
			n++
		}
		return "-" + Compact(-n)
	}
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	v, i := float64(n)/1000, 0
	for {
		r := round(v)
		if r >= 1000 && i < len(units)-1 {
			v, i = v/1000, i+1
			continue
		}
		return strings.TrimSuffix(strconv.FormatFloat(r, 'f', 1, 64), ".0") + units[i]
	}
}

// round keeps one decimal below 10 and whole numbers from 10.
func round(v float64) float64 {
	if v < 10 {
		return math.Round(v*10) / 10
	}
	return math.Round(v)
}

// Exact writes n with thousands separators (1,234,567) for tooltips.
func Exact(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		s = "-" + s
	}
	return s
}
