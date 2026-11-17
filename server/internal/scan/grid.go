package scan

import (
	"math"

	"hometwin/internal/home"
)

// grid is a raster over the floor plane, cell metres square, cell (0,0) at (x0, z0).
type grid struct {
	x0, z0, cell float64
	nx, nz       int
}

func newGrid(ts []tri, cell, pad float64) grid {
	x0, z0, x1, z1 := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, t := range ts {
		for _, p := range t.p {
			x0, x1 = math.Min(x0, p[0]), math.Max(x1, p[0])
			z0, z1 = math.Min(z0, p[2]), math.Max(z1, p[2])
		}
	}
	x0, z0 = x0-pad, z0-pad
	return grid{x0: x0, z0: z0, cell: cell, nx: int((x1+pad-x0)/cell) + 1, nz: int((z1+pad-z0)/cell) + 1}
}

func (g grid) n() int { return g.nx * g.nz }

func (g grid) at(x, z float64) (int, bool) {
	i, j := int(math.Floor((x-g.x0)/g.cell)), int(math.Floor((z-g.z0)/g.cell))
	if i < 0 || j < 0 || i >= g.nx || j >= g.nz {
		return 0, false
	}
	return j*g.nx + i, true
}

func (g grid) centre(k int) home.P {
	return home.P{g.x0 + (float64(k%g.nx)+0.5)*g.cell, g.z0 + (float64(k/g.nx)+0.5)*g.cell}
}

// line marks the cells a segment passes through.
func (g grid) line(m []bool, s seg) {
	dx, dz := s[1][0]-s[0][0], s[1][1]-s[0][1]
	n := int(math.Hypot(dx, dz)/(g.cell/2)) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		if k, ok := g.at(s[0][0]+dx*t, s[0][1]+dz*t); ok {
			m[k] = true
		}
	}
}

// dilate grows the marked cells by r cells (square).
func (g grid) dilate(m []bool, r int) []bool {
	tmp := make([]bool, len(m))
	for j := range g.nz {
		for i := range g.nx {
			for d := -r; d <= r; d++ {
				if ii := i + d; ii >= 0 && ii < g.nx && m[j*g.nx+ii] {
					tmp[j*g.nx+i] = true
					break
				}
			}
		}
	}
	out := make([]bool, len(m))
	for j := range g.nz {
		for i := range g.nx {
			for d := -r; d <= r; d++ {
				if jj := j + d; jj >= 0 && jj < g.nz && tmp[jj*g.nx+i] {
					out[j*g.nx+i] = true
					break
				}
			}
		}
	}
	return out
}

// erode shrinks the marked cells by r cells (square).
func (g grid) erode(m []bool, r int) []bool {
	inv := make([]bool, len(m))
	for k, v := range m {
		inv[k] = !v
	}
	d := g.dilate(inv, r)
	for k, v := range d {
		d[k] = !v
	}
	return d
}

// label numbers the 4-connected components of the cells where free is true, from 1;
// 0 marks the rest. It returns the labels and the component sizes (index = label).
func (g grid) label(free []bool) ([]int32, []int) {
	lab := make([]int32, len(free))
	sizes := []int{0}
	stack := []int{}
	for s := range free {
		if !free[s] || lab[s] != 0 {
			continue
		}
		id := int32(len(sizes))
		sizes = append(sizes, 0)
		lab[s] = id
		stack = append(stack[:0], s)
		for len(stack) > 0 {
			k := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			sizes[id]++
			i, j := k%g.nx, k/g.nx
			for _, nb := range [4][2]int{{i + 1, j}, {i - 1, j}, {i, j + 1}, {i, j - 1}} {
				if nb[0] < 0 || nb[1] < 0 || nb[0] >= g.nx || nb[1] >= g.nz {
					continue
				}
				q := nb[1]*g.nx + nb[0]
				if free[q] && lab[q] == 0 {
					lab[q] = id
					stack = append(stack, q)
				}
			}
		}
	}
	return lab, sizes
}

// largest keeps the biggest 4-connected blob of the marked cells.
func (g grid) largest(m []bool) []bool {
	lab, sizes := g.label(m)
	best := 0
	for l := 1; l < len(sizes); l++ {
		if best == 0 || sizes[l] > sizes[best] {
			best = l
		}
	}
	out := make([]bool, len(m))
	for k, l := range lab {
		out[k] = best != 0 && l == int32(best)
	}
	return out
}

// dist is each cell's chamfer distance in metres to the nearest marked cell.
func (g grid) dist(m []bool) []float64 {
	const big = 1e9
	d := make([]float64, len(m))
	for k := range d {
		if !m[k] {
			d[k] = big
		}
	}
	c, cd := g.cell, g.cell*math.Sqrt2
	relax := func(k, i, j, di, dj int, w float64) {
		ii, jj := i+di, j+dj
		if ii >= 0 && jj >= 0 && ii < g.nx && jj < g.nz {
			if v := d[jj*g.nx+ii] + w; v < d[k] {
				d[k] = v
			}
		}
	}
	for j := range g.nz {
		for i := range g.nx {
			k := j*g.nx + i
			relax(k, i, j, -1, 0, c)
			relax(k, i, j, 0, -1, c)
			relax(k, i, j, -1, -1, cd)
			relax(k, i, j, 1, -1, cd)
		}
	}
	for j := g.nz - 1; j >= 0; j-- {
		for i := g.nx - 1; i >= 0; i-- {
			k := j*g.nx + i
			relax(k, i, j, 1, 0, c)
			relax(k, i, j, 0, 1, c)
			relax(k, i, j, 1, 1, cd)
			relax(k, i, j, -1, 1, cd)
		}
	}
	return d
}

// outline traces the outer boundary of the cells where in is true (one 4-connected blob
// assumed: it follows the first one met) along cell edges, as a closed polygon of cell corners.
func (g grid) outline(in []bool) []home.P {
	has := func(i, j int) bool { return i >= 0 && j >= 0 && i < g.nx && j < g.nz && in[j*g.nx+i] }
	// Start at the top-left cell of the blob: its top edge is on the outer boundary.
	start := -1
	for k := range in {
		if in[k] {
			start = k
			break
		}
	}
	if start < 0 {
		return nil
	}
	// Walk the cell corners keeping the blob on the right (x right, z down), recording the
	// corners where the way turns. Directions: 0 +x, 1 +z, 2 −x, 3 −z. Corner (ci, cj) is
	// the top-left corner of cell (ci, cj).
	ci, cj := start%g.nx, start/g.nx
	si, sj := ci, cj
	dir := 0
	var pts []home.P
	for steps := 0; steps < 4*len(in)+8; steps++ {
		// The cells ahead-left and ahead-right of the corner, moving in dir.
		var l, r bool
		switch dir {
		case 0:
			l, r = has(ci, cj-1), has(ci, cj)
		case 1:
			l, r = has(ci, cj), has(ci-1, cj)
		case 2:
			l, r = has(ci-1, cj), has(ci-1, cj-1)
		case 3:
			l, r = has(ci-1, cj-1), has(ci, cj-1)
		}
		nd := dir
		switch {
		case l:
			nd = (dir + 3) % 4
		case !r:
			nd = (dir + 1) % 4
		}
		if nd != dir || steps == 0 {
			pts = append(pts, home.P{g.x0 + float64(ci)*g.cell, g.z0 + float64(cj)*g.cell})
		}
		dir = nd
		switch dir {
		case 0:
			ci++
		case 1:
			cj++
		case 2:
			ci--
		case 3:
			cj--
		}
		if ci == si && cj == sj {
			break
		}
	}
	return dedupeCollinear(pts)
}

func dedupeCollinear(p []home.P) []home.P {
	var out []home.P
	n := len(p)
	for i := range n {
		a, b, c := p[(i+n-1)%n], p[i], p[(i+1)%n]
		if (a[0] == b[0] && b[0] == c[0]) || (a[1] == b[1] && b[1] == c[1]) {
			continue
		}
		out = append(out, b)
	}
	return out
}
