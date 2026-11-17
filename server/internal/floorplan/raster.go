package floorplan

import (
	"image"
	"math"
)

// raster is a grey copy of the plan, one byte of luma per pixel.
type raster struct {
	w, h int
	px   []uint8
}

func gray(img image.Image) *raster {
	b := img.Bounds()
	r := &raster{w: b.Dx(), h: b.Dy(), px: make([]uint8, b.Dx()*b.Dy())}
	for y := 0; y < r.h; y++ {
		for x := 0; x < r.w; x++ {
			cr, cg, cb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			r.px[y*r.w+x] = uint8((299*cr + 587*cg + 114*cb) / 1000 >> 8)
		}
	}
	return r
}

func (r *raster) at(x, y int) uint8 {
	if x < 0 || y < 0 || x >= r.w || y >= r.h {
		return 255
	}
	return r.px[y*r.w+x]
}

// Drawing finds the drawing in a picture: the largest light block, so a screenshot loses the
// app's dark bars around the plan. The whole picture when there is nothing to cut away.
func Drawing(img image.Image) image.Rectangle {
	b := img.Bounds()
	g := gray(img)
	light := func(x, y int) bool { return g.at(x, y) >= 200 }
	// rows with a fair share of light pixels, then the longest run of them
	rows := make([]bool, g.h)
	for y := range g.h {
		n := 0
		for x := range g.w {
			if light(x, y) {
				n++
			}
		}
		rows[y] = n*4 >= g.w
	}
	y0, y1 := longestRun(rows)
	cols := make([]bool, g.w)
	for x := range g.w {
		n := 0
		for y := y0; y < y1; y++ {
			if light(x, y) {
				n++
			}
		}
		cols[x] = n*4 >= y1-y0
	}
	x0, x1 := longestRun(cols)
	if y1-y0 < g.h/5 || x1-x0 < g.w/5 || (x1-x0)*(y1-y0)*100 >= 95*g.w*g.h {
		return b
	}
	return image.Rect(b.Min.X+x0, b.Min.Y+y0, b.Min.X+x1, b.Min.Y+y1)
}

func longestRun(v []bool) (int, int) {
	bi, bj := 0, len(v)
	best := -1
	for i := 0; i < len(v); {
		if !v[i] {
			i++
			continue
		}
		j := i
		for j < len(v) && v[j] {
			j++
		}
		if j-i > best {
			best, bi, bj = j-i, i, j
		}
		i = j
	}
	return bi, bj
}

// side is which edge a dimension chain runs along.
func horizontal(side string) bool { return side == "top" || side == "bottom" }

// lines returns the rows (top/bottom) or columns (left/right) that may be a dimension line:
// rows with long grey strokes in the outer 40%, outermost first, one per group of
// neighbouring rows.
func (r *raster) lines(side string) []int {
	n, length := r.h, r.w
	if !horizontal(side) {
		n, length = r.w, r.h
	}
	ink := func(i, j int) bool { // i across the line, j along it
		var v uint8
		if horizontal(side) {
			v = r.at(j, i)
		} else {
			v = r.at(i, j)
		}
		return v > 40 && v < 190
	}
	minRun := max(10, length/120)
	score := func(i int) int {
		s, run := 0, 0
		for j := 0; j <= length; j++ {
			if j < length && ink(i, j) {
				run++
				continue
			}
			if run >= minRun {
				s += run
			}
			run = 0
		}
		return s
	}
	var out []int
	last, lastScore := -10, 0
	for k := range n * 2 / 5 {
		i := k
		if side == "bottom" || side == "right" {
			i = n - 1 - k
		}
		s := score(i)
		if s*100 < 12*length {
			continue
		}
		if abs(i-last) <= 3 && len(out) > 0 {
			if s > lastScore {
				out[len(out)-1], lastScore = i, s
			}
		} else {
			out = append(out, i)
			lastScore = s
		}
		last = i
	}
	return out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ticks are the positions along a dimension line where a short bar crosses it: the chain's
// tick marks, and the upright strokes of its digits, which the fit later sorts out.
func (r *raster) ticks(side string, ln int) []float64 {
	length := r.w
	if !horizontal(side) {
		length = r.h
	}
	dark := func(i, j int) bool {
		if horizontal(side) {
			return r.at(j, i) < 170
		}
		return r.at(i, j) < 170
	}
	arm := max(5, int(math.Round(float64(max(r.w, r.h))/220)))
	count := func(j, from, to int) int {
		n := 0
		for i := from; i <= to; i++ {
			if dark(ln+i, j) {
				n++
			}
		}
		return n
	}
	need := max(3, arm/2)
	score := make([]int, length)
	for j := range length {
		a, b := count(j, -arm, -2), count(j, 2, arm)
		if a >= need && b >= need {
			score[j] = a + b
		}
	}
	var out []float64
	for j := 0; j < length; {
		if score[j] == 0 {
			j++
			continue
		}
		k := j
		for k < length && (score[k] > 0 || (k+1 < length && score[k+1] > 0)) {
			k++
		}
		// the tallest column of the cluster is the bar; arrowheads are lower
		top, sum, n := 0, 0.0, 0
		for i := j; i < k; i++ {
			top = max(top, score[i])
		}
		for i := j; i < k; i++ {
			if score[i] == top {
				sum += float64(i)
				n++
			}
		}
		out = append(out, sum/float64(n))
		j = k
	}
	return out
}

// mask marks the thick dark strokes of walls: near-black pixels that survive an opening of
// size win, which removes text, furniture outlines and dimension lines.
func (r *raster) mask(win int) []bool {
	m := make([]bool, r.w*r.h)
	for i, v := range r.px {
		m[i] = v < 70
	}
	m = morph(m, r.w, r.h, win, true)
	return morph(m, r.w, r.h, win, false)
}

// morph erodes (or dilates) a mask with a win×win square, one axis after the other.
func morph(m []bool, w, h, win int, erode bool) []bool {
	half := win / 2
	pass := func(src []bool, horiz bool) []bool {
		out := make([]bool, len(src))
		for y := range h {
			for x := range w {
				v := erode
				for d := -half; d <= half; d++ {
					xx, yy := x, y
					if horiz {
						xx += d
					} else {
						yy += d
					}
					s := !erode
					if xx >= 0 && yy >= 0 && xx < w && yy < h {
						s = src[yy*w+xx]
					} else if erode {
						s = false
					}
					if erode && !s {
						v = false
						break
					}
					if !erode && s {
						v = true
						break
					}
				}
				out[y*w+x] = v
			}
		}
		return out
	}
	return pass(pass(m, true), false)
}

// band looks for a wall stroke in the mask near a line: across is the line's pixel position
// (x of a vertical line), from/to its extent along. It returns the stroke's centre and width
// and how much of the extent it covers.
func band(m []bool, w, h int, vertical bool, across, from, to float64, reach int) (centre, width, cover float64, ok bool) {
	if to < from {
		from, to = to, from
	}
	pad := (to - from) * 0.1
	a0, a1 := int(from+pad), int(to-pad)
	if a1-a0 < 3 {
		return 0, 0, 0, false
	}
	c := int(math.Round(across))
	frac := make([]float64, 2*reach+1)
	for k := -reach; k <= reach; k++ {
		n, tot := 0, 0
		for a := a0; a <= a1; a++ {
			x, y := c+k, a
			if !vertical {
				x, y = a, c+k
			}
			if x < 0 || y < 0 || x >= w || y >= h {
				continue
			}
			tot++
			if m[y*w+x] {
				n++
			}
		}
		if tot > 0 {
			frac[k+reach] = float64(n) / float64(tot)
		}
	}
	// runs of columns that the stroke covers, the one nearest the line wins
	type run struct{ i, j int }
	var runs []run
	for i := 0; i < len(frac); {
		if frac[i] < 0.2 {
			i++
			continue
		}
		j := i
		for j < len(frac) && frac[j] >= 0.2 {
			j++
		}
		runs = append(runs, run{i, j})
		i = j
	}
	if len(runs) == 0 {
		return 0, 0, 0, false
	}
	// a full stroke a little further away beats a scrap nearby
	best, bs := runs[0], -1.0
	for _, rn := range runs {
		top := 0.0
		for i := rn.i; i < rn.j; i++ {
			top = math.Max(top, frac[i])
		}
		d := math.Abs(float64(rn.i+rn.j-1)/2 - float64(reach))
		if s := top * math.Exp(-d/float64(reach)); s > bs {
			best, bs, cover = rn, s, top
		}
	}
	return float64(c-reach) + float64(best.i+best.j-1)/2, float64(best.j - best.i), cover, true
}

// crop is the part of img inside rect, as its own image starting at (0, 0).
func crop(img image.Image, rect image.Rectangle) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := 0; y < rect.Dy(); y++ {
		for x := 0; x < rect.Dx(); x++ {
			out.Set(x, y, img.At(rect.Min.X+x, rect.Min.Y+y))
		}
	}
	return out
}
