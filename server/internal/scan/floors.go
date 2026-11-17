package scan

import (
	"math"
	"sort"
)

// floorMap is the lowest upward-facing surface near floor height per cell, filled in under
// furniture and walls from the nearest cell that has one.
type floorMap struct {
	g grid
	y []float64
}

// Floors lower than −0.4 m or higher than 0.36 m above the main floor are not floors: sunken
// bathrooms and platforms stay well inside, bed and sofa tops start around 0.42 m.
const floorLo, floorHi = -0.4, 0.36

func upFacing(t tri) bool { return t.n[1] > 0.95 }

func newFloorMap(ts []tri, g grid) floorMap {
	fm := floorMap{g: g, y: make([]float64, g.n())}
	for k := range fm.y {
		fm.y[k] = math.NaN()
	}
	for _, t := range ts {
		if !upFacing(t) {
			continue
		}
		y := t.centre()[1]
		if y < floorLo || y > floorHi {
			continue
		}
		eachCell(g, t, func(k int) {
			if math.IsNaN(fm.y[k]) || y < fm.y[k] {
				fm.y[k] = y
			}
		})
	}
	// Fill the gaps breadth first.
	q := make([]int, 0, len(fm.y))
	for k, v := range fm.y {
		if !math.IsNaN(v) {
			q = append(q, k)
		}
	}
	if len(q) == 0 {
		for k := range fm.y {
			fm.y[k] = 0
		}
		return fm
	}
	for h := 0; h < len(q); h++ {
		k := q[h]
		i, j := k%g.nx, k/g.nx
		for _, nb := range [4][2]int{{i + 1, j}, {i - 1, j}, {i, j + 1}, {i, j - 1}} {
			if nb[0] < 0 || nb[1] < 0 || nb[0] >= g.nx || nb[1] >= g.nz {
				continue
			}
			n := nb[1]*g.nx + nb[0]
			if math.IsNaN(fm.y[n]) {
				fm.y[n] = fm.y[k]
				q = append(q, n)
			}
		}
	}
	return fm
}

// newCeilingMap is the highest downward-facing surface over each cell, at least 1.85 m up,
// then the highest within r cells: the ceiling, not the beams or door heads under it. Cells
// without one get +Inf.
func newCeilingMap(ts []tri, g grid, r int) floorMap {
	cm := floorMap{g: g, y: make([]float64, g.n())}
	for k := range cm.y {
		cm.y[k] = math.Inf(-1)
	}
	for _, t := range ts {
		if t.n[1] > -0.95 {
			continue
		}
		y := t.centre()[1]
		if y < 1.85 {
			continue
		}
		eachCell(g, t, func(k int) { cm.y[k] = math.Max(cm.y[k], y) })
	}
	cm = cm.highest(r)
	for k, v := range cm.y {
		if math.IsInf(v, -1) {
			cm.y[k] = math.Inf(1)
		}
	}
	return cm
}

// highest is the map with each cell raised to the highest floor within r cells, so a wall
// between two levels is cut relative to the upper one.
func (fm floorMap) highest(r int) floorMap {
	g := fm.g
	out := floorMap{g: g, y: make([]float64, len(fm.y))}
	for j := range g.nz {
		for i := range g.nx {
			v := math.Inf(-1)
			for dj := -r; dj <= r; dj++ {
				for di := -r; di <= r; di++ {
					if ii, jj := i+di, j+dj; ii >= 0 && jj >= 0 && ii < g.nx && jj < g.nz {
						v = math.Max(v, fm.y[jj*g.nx+ii])
					}
				}
			}
			out.y[j*g.nx+i] = v
		}
	}
	return out
}

func (fm floorMap) at(x, z float64) float64 {
	if k, ok := fm.g.at(x, z); ok {
		return fm.y[k]
	}
	return 0
}

// eachCell calls fn for every grid cell whose centre lies inside the triangle's footprint,
// and for the cell under its centre (so small triangles count too).
func eachCell(g grid, t tri, fn func(k int)) {
	a, b, c := [2]float64{t.p[0][0], t.p[0][2]}, [2]float64{t.p[1][0], t.p[1][2]}, [2]float64{t.p[2][0], t.p[2][2]}
	x0, x1 := math.Min(a[0], math.Min(b[0], c[0])), math.Max(a[0], math.Max(b[0], c[0]))
	z0, z1 := math.Min(a[1], math.Min(b[1], c[1])), math.Max(a[1], math.Max(b[1], c[1]))
	i0, i1 := int(math.Floor((x0-g.x0)/g.cell-0.5)), int(math.Ceil((x1-g.x0)/g.cell-0.5))
	j0, j1 := int(math.Floor((z0-g.z0)/g.cell-0.5)), int(math.Ceil((z1-g.z0)/g.cell-0.5))
	edge := func(p, q, r [2]float64) float64 { return (q[0]-p[0])*(r[1]-p[1]) - (q[1]-p[1])*(r[0]-p[0]) }
	area := edge(a, b, c)
	hit := false
	for j := max(j0, 0); j <= min(j1, g.nz-1); j++ {
		for i := max(i0, 0); i <= min(i1, g.nx-1); i++ {
			p := [2]float64{g.x0 + (float64(i)+0.5)*g.cell, g.z0 + (float64(j)+0.5)*g.cell}
			w0, w1, w2 := edge(b, c, p), edge(c, a, p), edge(a, b, p)
			if area < 0 {
				w0, w1, w2 = -w0, -w1, -w2
			}
			if w0 >= 0 && w1 >= 0 && w2 >= 0 {
				fn(j*g.nx + i)
				hit = true
			}
		}
	}
	if !hit {
		ce := t.centre()
		if k, ok := g.at(ce[0], ce[2]); ok {
			fn(k)
		}
	}
}

// heightPeaks finds the heights that carry most area in a list of (height, area) pairs:
// a 1 cm histogram smoothed over ±1 cm, local maxima holding at least minArea within ±2 cm,
// at least sep apart, heaviest first, each refined to the weighted mean within ±2 cm.
func heightPeaks(ys, ws []float64, lo, hi, minArea, sep float64) []peak {
	const bin = 0.01
	n := int((hi-lo)/bin) + 1
	h := make([]float64, n)
	for i, y := range ys {
		if y >= lo && y < hi {
			h[int((y-lo)/bin)] += ws[i]
		}
	}
	s := make([]float64, n)
	for i := range h {
		for d := -1; d <= 1; d++ {
			if i+d >= 0 && i+d < n {
				s[i] += h[i+d]
			}
		}
	}
	var cand []peak
	for i := range s {
		if (i > 0 && s[i-1] > s[i]) || (i+1 < n && s[i+1] >= s[i]) {
			continue
		}
		c := lo + (float64(i)+0.5)*bin
		var sw, sy float64
		for k, y := range ys {
			if math.Abs(y-c) <= 0.02 {
				sw += ws[k]
				sy += y * ws[k]
			}
		}
		if sw >= minArea {
			cand = append(cand, peak{Y: sy / sw, Area: sw})
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].Area > cand[j].Area })
	var out []peak
	for _, c := range cand {
		ok := true
		for _, o := range out {
			if math.Abs(o.Y-c.Y) < sep {
				ok = false
			}
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
}

type peak struct{ Y, Area float64 }
