package floorplan

import (
	"math"
	"sort"
)

// chainFit is a dimension chain found in the image: where each of its tick marks is.
type chainFit struct {
	side  string
	mm    []float64 // cumulative, from the chain's first tick
	px    []float64 // each tick's pixel position along the line
	found []bool    // whether the tick was seen (or only predicted by the fit)
	k, b  float64   // px = k·mm + b
	model []float64 // where the model put each tick (NaN when it did not say)
	line  int
}

// fitChain matches a chain's ticks to the bars found along a line: the scale and offset that
// put the most ticks on a bar. Digits also make bars, but they do not fall on a straight
// scale with the chain's numbers.
func fitChain(mm []float64, cands []float64, span float64) (k, b float64, hit []int, ok bool) {
	n := len(mm)
	if n < 3 || len(cands) < 2 {
		return 0, 0, nil, false
	}
	tol := math.Max(3, span/250)
	bestN, bestErr := 0, math.Inf(1)
	near := func(p float64) (int, float64) {
		i := sort.SearchFloat64s(cands, p)
		bi, bd := -1, math.Inf(1)
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(cands) && math.Abs(cands[j]-p) < bd {
				bi, bd = j, math.Abs(cands[j]-p)
			}
		}
		return bi, bd
	}
	for a := range cands {
		for c := a + 1; c < len(cands); c++ {
			for i := range n {
				for j := i + 1; j < n; j++ {
					kk := (cands[c] - cands[a]) / (mm[j] - mm[i])
					if kk < 1.0/80 || kk > 1.0/3 { // 3 to 80 mm a pixel
						continue
					}
					bb := cands[a] - kk*mm[i]
					cnt, e := 0, 0.0
					for _, m := range mm {
						if _, d := near(kk*m + bb); d <= tol {
							cnt++
							e += d
						}
					}
					if cnt > bestN || (cnt == bestN && e < bestErr) {
						bestN, bestErr, k, b = cnt, e, kk, bb
					}
				}
			}
		}
	}
	if bestN < 3 || bestN*3 < n*2 {
		return 0, 0, nil, false
	}
	// least squares on the ticks that matched
	hit = make([]int, n)
	var sx, sy, sxx, sxy, cnt float64
	for i, m := range mm {
		j, d := near(k*m + b)
		hit[i] = -1
		if d <= tol {
			hit[i] = j
			p := cands[j]
			sx, sy, sxx, sxy, cnt = sx+m, sy+p, sxx+m*m, sxy+m*p, cnt+1
		}
	}
	if den := cnt*sxx - sx*sx; den > 0 {
		k = (cnt*sxy - sx*sy) / den
		b = (sy - k*sx) / cnt
	}
	return k, b, hit, true
}

// findChain locates one chain of the reading in the image.
func findChain(r *raster, side string, c Chain) (chainFit, bool) {
	if len(c.Segments) == 0 {
		return chainFit{}, false
	}
	f := chainFit{side: side, mm: []float64{0}, model: []float64{c.Segments[0].From}}
	for i, s := range c.Segments {
		f.mm = append(f.mm, f.mm[i]+s.MM)
		to := s.To
		if i+1 < len(c.Segments) && c.Segments[i+1].From > 0 && to > 0 {
			to = (to + c.Segments[i+1].From) / 2
		}
		f.model = append(f.model, to)
	}
	for _, ln := range r.lines(side) {
		cands := r.ticks(side, ln)
		k, b, hit, ok := fitChain(f.mm, cands, float64(max(r.w, r.h)))
		if !ok {
			continue
		}
		f.k, f.b, f.line = k, b, ln
		f.px, f.found = make([]float64, len(f.mm)), make([]bool, len(f.mm))
		for i, m := range f.mm {
			if hit[i] >= 0 {
				f.px[i], f.found[i] = cands[hit[i]], true
			} else {
				f.px[i] = k*m + b
			}
		}
		return f, true
	}
	return f, false
}

// gridLine is a line of the plan's grid: a pixel position and the millimetres it stands for.
type gridLine struct {
	px, mm float64
	chain  bool // from a dimension chain (exact) rather than measured
}

// axis maps one direction of the image to millimetres.
type axis struct {
	mmPerPx float64
	origin  float64 // px of mm 0
	lines   []gridLine
	// a, c map the model's pixel positions onto the image's: true = a·model + c, fitted on the
	// wall strokes nearest the model's walls (the model's own tick positions are no better
	// than its other positions)
	a, c float64
}

// mm turns a pixel position into millimetres through the grid lines around it.
func (ax *axis) mm(px float64) float64 {
	ls := ax.lines
	if len(ls) == 0 {
		return (px - ax.origin) * ax.mmPerPx
	}
	if px <= ls[0].px {
		return ls[0].mm + (px-ls[0].px)*ax.mmPerPx
	}
	for i := 1; i < len(ls); i++ {
		if px <= ls[i].px {
			t := (px - ls[i-1].px) / (ls[i].px - ls[i-1].px)
			return ls[i-1].mm + t*(ls[i].mm-ls[i-1].mm)
		}
	}
	l := ls[len(ls)-1]
	return l.mm + (px-l.px)*ax.mmPerPx
}

// px is the inverse of mm.
func (ax *axis) px(mm float64) float64 {
	ls := ax.lines
	if len(ls) == 0 {
		return ax.origin + mm/ax.mmPerPx
	}
	if mm <= ls[0].mm {
		return ls[0].px + (mm-ls[0].mm)/ax.mmPerPx
	}
	for i := 1; i < len(ls); i++ {
		if mm <= ls[i].mm {
			t := (mm - ls[i-1].mm) / (ls[i].mm - ls[i-1].mm)
			return ls[i-1].px + t*(ls[i].px-ls[i-1].px)
		}
	}
	l := ls[len(ls)-1]
	return l.px + (mm-l.mm)/ax.mmPerPx
}

// fix maps a model pixel position onto the image.
func (ax *axis) fix(v float64) float64 { return ax.a*v + ax.c }

// buildAxis makes an axis from the chains found on its two sides (either may be missing).
// When none was found in the image, the model's own tick positions stand in.
func buildAxis(fits []chainFit, modelOnly []chainFit) (axis, bool) {
	ax := axis{a: 1}
	if len(fits) == 0 {
		// no chain seen in the image: trust the model's tick positions
		for _, f := range modelOnly {
			var xs, ys []float64
			for i, p := range f.model {
				if !math.IsNaN(p) && p > 0 {
					xs, ys = append(xs, f.mm[i]), append(ys, p)
				}
			}
			if k, b, ok := lsq(xs, ys); ok && k > 0 {
				f.k, f.b = k, b
				f.px = make([]float64, len(f.mm))
				for i, m := range f.mm {
					f.px[i] = k*m + b
				}
				fits = append(fits, f)
			}
		}
		if len(fits) == 0 {
			return ax, false
		}
	}
	var ks, ws float64
	ax.origin = math.Inf(1)
	for _, f := range fits {
		ks += f.k * float64(len(f.mm))
		ws += float64(len(f.mm))
		ax.origin = math.Min(ax.origin, f.px[0])
	}
	ax.mmPerPx = ws / ks
	// every chain's ticks, in millimetres from the axis' origin
	var all []gridLine
	for _, f := range fits {
		off := round10((f.px[0] - ax.origin) * ax.mmPerPx)
		for i, m := range f.mm {
			all = append(all, gridLine{px: f.px[i], mm: off + m, chain: true})
		}
	}
	ax.lines = mergeLines(all, 120/ax.mmPerPx)
	return ax, true
}

// mergeLines joins lines closer than tol pixels; their millimetres are averaged.
func mergeLines(ls []gridLine, tol float64) []gridLine {
	sort.Slice(ls, func(i, j int) bool { return ls[i].px < ls[j].px })
	var out []gridLine
	for i := 0; i < len(ls); {
		j := i
		var px, mm float64
		chain := false
		for j < len(ls) && ls[j].px-ls[i].px <= tol {
			px += ls[j].px
			mm += ls[j].mm
			chain = chain || ls[j].chain
			j++
		}
		n := float64(j - i)
		out = append(out, gridLine{px: px / n, mm: round10(mm / n), chain: chain})
		i = j
	}
	// millimetres must rise with pixels
	for i := 1; i < len(out); i++ {
		if out[i].mm <= out[i-1].mm {
			out[i].mm = out[i-1].mm + 10
		}
	}
	return out
}

// addLine puts a measured line into the axis unless a line is already within tol pixels.
func (ax *axis) addLine(px, tol float64) {
	for _, l := range ax.lines {
		if math.Abs(l.px-px) <= tol {
			return
		}
	}
	mm := round10(ax.mm(px))
	ax.lines = append(ax.lines, gridLine{px: px, mm: mm})
	sort.Slice(ax.lines, func(i, j int) bool { return ax.lines[i].px < ax.lines[j].px })
}

// snap is the grid line nearest px within tol pixels, or the position itself in millimetres.
func (ax *axis) snap(px, tol float64) (float64, bool) {
	best, bd := 0.0, math.Inf(1)
	for _, l := range ax.lines {
		if d := math.Abs(l.px - px); d < bd {
			best, bd = l.mm, d
		}
	}
	if bd <= tol {
		return best, true
	}
	return round10(ax.mm(px)), false
}

func round10(v float64) float64 { return math.Round(v/10) * 10 }

func lsq(xs, ys []float64) (k, b float64, ok bool) {
	n := float64(len(xs))
	if n < 2 {
		return 0, 0, false
	}
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx, sy, sxx, sxy = sx+xs[i], sy+ys[i], sxx+xs[i]*xs[i], sxy+xs[i]*ys[i]
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, 0, false
	}
	k = (n*sxy - sx*sy) / den
	return k, (sy - k*sx) / n, true
}

// robustLine fits y = a·x + c through the pairs most of them agree on (within tol).
func robustLine(xs, ys []float64, tol float64) (a, c float64, ok bool) {
	best := 0
	for i := range xs {
		for j := i + 1; j < len(xs); j++ {
			if xs[j] == xs[i] {
				continue
			}
			aa := (ys[j] - ys[i]) / (xs[j] - xs[i])
			cc := ys[i] - aa*xs[i]
			n := 0
			for q := range xs {
				if math.Abs(aa*xs[q]+cc-ys[q]) <= tol {
					n++
				}
			}
			if n > best {
				best, a, c = n, aa, cc
			}
		}
	}
	if best < 3 {
		return 0, 0, false
	}
	var ix, iy []float64
	for q := range xs {
		if math.Abs(a*xs[q]+c-ys[q]) <= tol {
			ix, iy = append(ix, xs[q]), append(iy, ys[q])
		}
	}
	return lsqOr(ix, iy, a, c)
}

func lsqOr(xs, ys []float64, a, c float64) (float64, float64, bool) {
	if k, b, ok := lsq(xs, ys); ok {
		return k, b, true
	}
	return a, c, true
}
