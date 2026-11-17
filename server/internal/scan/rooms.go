package scan

import (
	"math"

	"hometwin/internal/home"
)

const (
	cellSize = 0.025
	// closeAbove is how high above the local floor the walls are cut to find closed rooms:
	// above door heads (2.0–2.1 m) and below ceilings, beams and curtain boxes.
	closeAbove = 2.2
	// gapCells closes the scan's small holes before rooms are filled.
	gapCells = 2
	minRoom  = 1.0 // m²
	minHall  = 0.6 // m², a passage left between rooms
	// minDepth: a room has a spot this far from every wall (wall cavities and wardrobes do not).
	minDepth = 0.45
)

// region is one closed room found in the scan.
type region struct {
	cells []bool
	area  float64 // m², of the cells
	poly  []home.P
}

// findRooms finds the closed rooms: the free floor between the walls cut at closeAbove over
// the local floor (or just under a lower ceiling), which closes the doors. Windows reaching above that cut are closed by
// lower cuts (0.3, 0.6, 1.0 m: sills, parapets, railings), taken only next to the outside
// so furniture does not split the rooms. Pockets too small to be rooms but too deep to be
// wall cavities (wardrobes and cupboards up to the ceiling) join the room they open onto.
//
// Seeds are points known to lie in named rooms (where the scan was taken from, or a floor
// plan's rooms). A region holding seeds of different rooms is cut again higher up, under
// the ceiling, until they come apart: a balcony behind a full-height opening, say.
func findRooms(ts []tri, fm floorMap, g grid, seeds []seed) []region {
	fm = fm.highest(3)
	cm := newCeilingMap(ts, fm.g, 3)
	cut := func(h float64) []bool {
		m := make([]bool, g.n())
		for _, s := range sliceAt(ts, func(x, z float64) float64 {
			// Under a low ceiling (bathrooms, kitchens) cut just below it.
			return math.Min(fm.at(x, z)+h, cm.at(x, z)-0.1)
		}) {
			g.line(m, s)
		}
		return m
	}
	wall := cut(closeAbove)
	all := append([]bool(nil), wall...)
	var lows [][]bool
	for _, h := range []float64{0.3, 0.6, 1.0} {
		m := cut(h)
		lows = append(lows, m)
		for k := range m {
			all[k] = all[k] || m[k]
		}
	}
	// Thin gaps (cavities between a wall's faces, slits) stay out of the outside.
	_, outside := closedRegions(g, g.dilate(all, 3), nil)
	near := g.dist(outside)
	for _, m := range lows {
		for k := range m {
			if m[k] && near[k] <= outsideBand {
				wall[k] = true
			}
		}
	}
	// What is outside with every cut is outside for good: a gap in a wall at all heights
	// (a missed bit of scan) must not let a room out.
	edge := g.dilate(outside, 1)
	for k := range edge {
		if edge[k] && !outside[k] {
			wall[k] = true
		}
	}
	regs, _ := closedRegions(g, wall, lows[2])
	regs = splitBySeeds(g, cut, wall, lows[2], regs, seeds)
	// Slivers a split leaves behind are not rooms.
	kept := regs[:0]
	for _, r := range regs {
		if r.area >= minHall {
			kept = append(kept, r)
		}
	}
	regs = kept
	out := regs[:0]
	for _, r := range regs {
		r.poly = squarePoly(g.outline(r.cells), 0.06, 0.25)
		if len(r.poly) >= 3 && polyArea(r.poly) >= minHall {
			out = append(out, r)
		}
	}
	return out
}

type seed struct {
	P    home.P
	Room string
}

// splitBySeeds cuts regions that hold seeds of more than one room, trying ever higher cuts.
func splitBySeeds(g grid, cut func(h float64) []bool, wall, low []bool, regs []region, seeds []seed) []region {
	var out []region
	for _, r := range regs {
		names := map[string]bool{}
		for _, s := range seeds {
			if k, ok := g.at(s.P[0], s.P[1]); ok && r.cells[k] {
				names[s.Room] = true
			}
		}
		if len(names) < 2 {
			out = append(out, r)
			continue
		}
		// Of the cuts that part the rooms, take the one leaving the most pieces.
		var best []region
		bestRaw := 0
		for _, h := range []float64{2.35, 2.5, 2.6, 2.7, 2.1, 2.05, 2.0} {
			m := cut(h)
			for k := range m {
				m[k] = m[k] || wall[k] || !r.cells[k]
			}
			parts, _ := closedRegions(g, m, low)
			// Parts holding seeds of one room join up; a part holding two rooms fails.
			byName := map[string]int{}
			var keep []region
			ok := true
			for _, p := range parts {
				pn := map[string]bool{}
				for _, s := range seeds {
					if k, in := g.at(s.P[0], s.P[1]); in && p.cells[k] {
						pn[s.Room] = true
					}
				}
				if len(pn) > 1 {
					ok = false
				}
				for n := range pn {
					i, seen := byName[n]
					if !seen {
						byName[n] = len(keep)
						keep = append(keep, region{cells: make([]bool, len(p.cells))})
						i = len(keep) - 1
					}
					for k, c := range p.cells {
						keep[i].cells[k] = keep[i].cells[k] || c
					}
				}
			}
			if ok && len(keep) >= 2 && len(parts) > bestRaw {
				best, bestRaw = keep, len(parts)
			}
		}
		if best == nil {
			out = append(out, r)
			continue
		}
		out = append(out, giveRest(g, r, best)...)
	}
	return out
}

// giveRest hands the cells of r that no part took to the nearest part; a leftover big
// enough for a room (a hall between the named rooms, say) becomes one.
func giveRest(g grid, r region, parts []region) []region {
	rest := append([]bool(nil), r.cells...)
	for _, p := range parts {
		for k, in := range p.cells {
			if in {
				rest[k] = false
			}
		}
	}
	// Margins left between the parts are thin; a hall survives a 12.5 cm erosion.
	core := g.erode(rest, 2*gapCells+1)
	lab, sizes := g.label(core)
	for l := 1; l < len(sizes); l++ {
		if float64(sizes[l])*g.cell*g.cell < minRoom/2 {
			continue
		}
		cells := make([]bool, len(rest))
		for k, v := range lab {
			cells[k] = v == int32(l)
		}
		cells = g.dilate(cells, 2*gapCells+1)
		for k := range cells {
			cells[k] = cells[k] && rest[k]
		}
		parts = append(parts, region{cells: cells})
	}
	owner := make([]int, len(r.cells))
	var q []int
	for k := range owner {
		owner[k] = -1
	}
	for i, p := range parts {
		for k, in := range p.cells {
			if in && r.cells[k] && owner[k] < 0 {
				owner[k] = i
				q = append(q, k)
			}
		}
	}
	for h := 0; h < len(q); h++ {
		k := q[h]
		i, j := k%g.nx, k/g.nx
		for _, nb := range [4][2]int{{i + 1, j}, {i - 1, j}, {i, j + 1}, {i, j - 1}} {
			if nb[0] < 0 || nb[1] < 0 || nb[0] >= g.nx || nb[1] >= g.nz {
				continue
			}
			n := nb[1]*g.nx + nb[0]
			if r.cells[n] && owner[n] < 0 {
				owner[n] = owner[k]
				q = append(q, n)
			}
		}
	}
	out := make([]region, len(parts))
	ca := g.cell * g.cell
	for i := range parts {
		out[i].cells = make([]bool, len(r.cells))
	}
	for k, o := range owner {
		if o >= 0 {
			out[o].cells[k] = true
			out[o].area += ca
		}
	}
	return out
}

// outsideBand is how far in from the outside low cuts count as walls.
const outsideBand = 0.35

// closedRegions fills the free space between walls; it returns the rooms and the cells that
// are outside (connected to the edge of the grid). With low (the walls cut at 1 m) a space
// too narrow for a room still counts as one, a hall, when at 1 m it opens onto a room;
// other spaces too small for a room are pockets (wardrobes) and join the room in front.
func closedRegions(g grid, wall, low []bool) ([]region, []bool) {
	pockets := low != nil
	closed := g.dilate(wall, gapCells)
	free := make([]bool, len(closed))
	for k := range closed {
		free[k] = !closed[k]
	}
	lab, sizes := g.label(free)
	border := map[int32]bool{}
	for i := range g.nx {
		border[lab[i]] = true
		border[lab[(g.nz-1)*g.nx+i]] = true
	}
	for j := range g.nz {
		border[lab[j*g.nx]] = true
		border[lab[j*g.nx+g.nx-1]] = true
	}
	outside := make([]bool, len(lab))
	for k, l := range lab {
		outside[k] = border[l] && l != 0
	}
	depth := g.dist(closed)
	deep := make([]bool, len(sizes))
	for k, l := range lab {
		if l != 0 && depth[k] >= minDepth {
			deep[l] = true
		}
	}
	ca := g.cell * g.cell
	var rooms []int
	isRoom := make([]bool, len(sizes))
	for l := 1; l < len(sizes); l++ {
		if !border[int32(l)] && deep[l] && float64(sizes[l])*ca >= minRoom {
			rooms = append(rooms, l)
			isRoom[l] = true
		}
	}
	if pockets {
		maxd := make([]float64, len(sizes))
		for k, l := range lab {
			maxd[l] = math.Max(maxd[l], depth[k])
		}
		lowFree := g.dilate(low, gapCells)
		for k := range lowFree {
			lowFree[k] = !lowFree[k]
		}
		ll, lsizes := g.label(lowFree)
		roomCells := make([]int, len(lsizes)) // per 1 m component: cells of rooms in it
		for k, l := range lab {
			if isRoom[l] && ll[k] != 0 {
				roomCells[ll[k]]++
			}
		}
		for l := 1; l < len(sizes); l++ {
			if isRoom[l] || border[int32(l)] || maxd[l] < hallDepth || float64(sizes[l])*ca < minRoom {
				continue
			}
			count := map[int32]int{}
			for k, v := range lab {
				if v == int32(l) && ll[k] != 0 {
					count[ll[k]]++
				}
			}
			best, bc := int32(0), 0
			for c, n := range count {
				if n > bc {
					best, bc = c, n
				}
			}
			if best != 0 && float64(roomCells[best])*ca >= minRoom {
				rooms = append(rooms, l)
				isRoom[l] = true
			}
		}
		// A pocket at least pocketDepth deep joins the room it shares most cells with
		// across the closing margin.
		r := 2*gapCells + 3 // reaches over a wardrobe's front, not over a wall
		for l := 1; l < len(sizes); l++ {
			if isRoom[l] || border[int32(l)] || maxd[l] < pocketDepth || float64(sizes[l])*ca < 0.1 {
				continue
			}
			votes := map[int32]int{}
			for k, v := range lab {
				if v != int32(l) {
					continue
				}
				i, j := k%g.nx, k/g.nx
				for _, d := range [4][2]int{{r, 0}, {-r, 0}, {0, r}, {0, -r}} {
					ii, jj := i+d[0], j+d[1]
					if ii >= 0 && jj >= 0 && ii < g.nx && jj < g.nz {
						if o := lab[jj*g.nx+ii]; o != 0 && isRoom[o] {
							votes[o]++
						}
					}
				}
			}
			best, bv := int32(0), 0
			for o, v := range votes {
				if v > bv {
					best, bv = o, v
				}
			}
			if best != 0 {
				for k, v := range lab {
					if v == int32(l) {
						lab[k] = best
					}
				}
			}
		}
	}
	var out []region
	for _, l := range rooms {
		cells := make([]bool, len(lab))
		for k, v := range lab {
			cells[k] = v == int32(l)
		}
		// Close the seams between a room and its pockets, then grow back to the walls.
		cells = growInto(g, cells, closed, wall, gapCells+1)
		cells = fillHoles(g, cells)
		n := 0
		for _, c := range cells {
			if c {
				n++
			}
		}
		out = append(out, region{cells: cells, area: float64(n) * ca})
	}
	return out, outside
}

// pocketDepth: half the thickness a closed pocket needs to be furniture rather than the
// cavity inside a wall, measured after the closing margin.
const pocketDepth = 0.15

// hallDepth: a hall is at least about 0.7 m wide (measured after the closing margin).
const hallDepth = 0.3

// growInto grows the region r cells back over the closing margin, stopping at the walls.
func growInto(g grid, cells, closed, wall []bool, r int) []bool {
	out := append([]bool(nil), cells...)
	front := []int{}
	for k, c := range cells {
		if c {
			front = append(front, k)
		}
	}
	for range r {
		var next []int
		for _, k := range front {
			i, j := k%g.nx, k/g.nx
			for _, nb := range [4][2]int{{i + 1, j}, {i - 1, j}, {i, j + 1}, {i, j - 1}} {
				if nb[0] < 0 || nb[1] < 0 || nb[0] >= g.nx || nb[1] >= g.nz {
					continue
				}
				q := nb[1]*g.nx + nb[0]
				if !out[q] && closed[q] && !wall[q] {
					out[q] = true
					next = append(next, q)
				}
			}
		}
		front = next
	}
	return out
}

// fillHoles fills what the region encloses (furniture standing free, columns, scan holes).
func fillHoles(g grid, cells []bool) []bool {
	inv := make([]bool, len(cells))
	for k, c := range cells {
		inv[k] = !c
	}
	lab, _ := g.label(inv)
	border := map[int32]bool{}
	for i := range g.nx {
		border[lab[i]] = true
		border[lab[(g.nz-1)*g.nx+i]] = true
	}
	for j := range g.nz {
		border[lab[j*g.nx]] = true
		border[lab[j*g.nx+g.nx-1]] = true
	}
	out := append([]bool(nil), cells...)
	for k, l := range lab {
		if l != 0 && !border[l] {
			out[k] = true
		}
	}
	return out
}

func polyArea(p []home.P) float64 { return math.Abs(signedArea(p)) }
