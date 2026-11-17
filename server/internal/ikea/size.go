package ikea

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var vulgar = strings.NewReplacer(
	"½", " 1/2", "¼", " 1/4", "¾", " 3/4", "⅛", " 1/8", "⅜", " 3/8", "⅝", " 5/8", "⅞", " 7/8",
	"⅓", " 1/3", "⅔", " 2/3", "⅙", " 1/6", "⅚", " 5/6", "⅕", " 1/5",
	" ", " ", " ", " ", " ", " ",
	"×", "x", "X", "x", "＊", "x", "*", "x",
	"″", `"`, "“", `"`, "”", `"`, "＂", `"`,
	"，", ",",
)

// normSize rewrites unicode fractions, multiplication signs and odd spaces to plain ASCII.
func normSize(s string) string {
	s = vulgar.Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

const numPat = `(\d+(?:[.,]\d+)?(?: \d+/\d+)?|\d+/\d+)`

var (
	unitPat = `("|inch(?:es)?\b|in\b|厘米|公分|cm\b|毫米|mm\b)`
	size3Re = regexp.MustCompile(numPat + ` ?x ?` + numPat + `(?: ?x ?` + numPat + `)? ?` + unitPat)
	size1Re = regexp.MustCompile(`^` + numPat + ` ?` + unitPat)
	massRe  = regexp.MustCompile(`^` + numPat + ` ?(lbs|lb|kg|公斤|千克)(?: ?` + numPat + ` ?oz)?`)
)

// number reads "31", "31.5", "31,5", "31 1/2" or "1/2".
func number(s string) (float64, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	whole, frac := s, ""
	if i := strings.IndexByte(s, ' '); i >= 0 {
		whole, frac = s[:i], s[i+1:]
	} else if strings.Contains(s, "/") {
		whole, frac = "0", s
	}
	v, err := strconv.ParseFloat(whole, 64)
	if err != nil {
		return 0, err
	}
	if frac != "" {
		a, b, ok := strings.Cut(frac, "/")
		if !ok {
			return 0, errors.New("bad fraction")
		}
		n, err1 := strconv.ParseFloat(a, 64)
		d, err2 := strconv.ParseFloat(b, 64)
		if err1 != nil || err2 != nil || d == 0 {
			return 0, errors.New("bad fraction")
		}
		v += n / d
	}
	return v, nil
}

// toCM converts a value in the unit as written on the page.
func toCM(v float64, unit string) float64 {
	switch unit {
	case `"`, "in", "inch", "inches":
		v *= 2.54
	case "mm", "毫米":
		v /= 10
	}
	return round1(v)
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// ParseSize finds the first "W x D x H unit" (or two-part "W x H unit") in s and returns the
// numbers in centimetres, in the order written.
func ParseSize(s string) ([]float64, error) {
	m := size3Re.FindStringSubmatch(normSize(s))
	if m == nil {
		return nil, errors.New("没有找到尺寸")
	}
	unit := m[4]
	var out []float64
	for _, part := range m[1:4] {
		if part == "" {
			continue
		}
		v, err := number(part)
		if err != nil {
			return nil, errors.New("尺寸看不懂：" + m[0])
		}
		out = append(out, toCM(v, unit))
	}
	return out, nil
}

// ParseLength reads one length ("37 ½ \"", "117 cm", "40 厘米") in centimetres.
func ParseLength(s string) (float64, bool) {
	m := size1Re.FindStringSubmatch(normSize(s))
	if m == nil {
		return 0, false
	}
	v, err := number(m[1])
	if err != nil {
		return 0, false
	}
	return toCM(v, m[2]), true
}

// parseMass reads "88 lb 1 oz", "29.50 kg" or "14 公斤" in kilograms.
func parseMass(s string) (float64, bool) {
	m := massRe.FindStringSubmatch(normSize(s))
	if m == nil {
		return 0, false
	}
	v, err := number(m[1])
	if err != nil {
		return 0, false
	}
	if m[2] == "lb" || m[2] == "lbs" {
		if m[3] != "" {
			if oz, err := number(m[3]); err == nil {
				v += oz / 16
			}
		}
		v *= 0.45359237
	}
	return math.Round(v*100) / 100, true
}
