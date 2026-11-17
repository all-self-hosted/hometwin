package scan

import (
	"fmt"
	"math"
	"sort"

	"hometwin/internal/home"
)

const (
	binLen  = 0.02 // along a wall
	rowStep = 0.05 // up a wall
	rowLo   = -0.4 // lowest row, m above the main floor
	rows    = 70   // up to 3.05 m
	// slack lets the cut lines count a little in front of a wall's faces.
	slack = 0.05
)

// openings finds the doors and windows in each wall by cutting the scan across the wall at
// every 5 cm of height and seeing where nothing stands between its two faces. A door is
// open from just above the floor to past 1.8 m, a window is open at 1.0–1.6 m with
// something under it.
//
// Walls along an unscanned room (one added from the floor plan) are left alone: the scan
// sees nothing there, which would read as one long opening.
func openings(ts []tri, fm floorMap, rooms []home.Room, ws []home.Wall, unscanned map[string]bool) ([]home.Door, []home.Window) {
	var doors []home.Door
	var wins []home.Window
	for _, w := range ws {
		l := w.Len()
		if l < 0.5 {
			continue
		}
		if len(unscanned) > 0 {
			mid, n := w.At(l/2), w.Normal()
			o := w.Thick/2 + 0.2
			a := roomAt(rooms, home.P{mid[0] + n[0]*o, mid[1] + n[1]*o})
			b := roomAt(rooms, home.P{mid[0] - n[0]*o, mid[1] - n[1]*o})
			if (a != nil && unscanned[a.ID]) || (b != nil && unscanned[b.ID]) {
				continue
			}
		}
		d, n := w.Dir(), w.Normal()
		nb := int(l/binLen) + 1
		cov := make([][rows]bool, nb)
		half := w.Thick/2 + slack
		// Wall-local coordinates: s along A→B, q across (along the normal).
		local := func(x, z float64) (float64, float64) {
			dx, dz := x-w.A[0], z-w.A[1]
			return dx*d[0] + dz*d[1], dx*n[0] + dz*n[1]
		}
		for _, t := range ts {
			if math.Abs(t.n[1]) > 0.9 {
				continue
			}
			smin, smax, qmin, qmax := math.Inf(1), math.Inf(-1), math.Inf(1), math.Inf(-1)
			ylo, yhi := math.Inf(1), math.Inf(-1)
			for _, p := range t.p {
				s, q := local(p[0], p[2])
				smin, smax, qmin, qmax = math.Min(smin, s), math.Max(smax, s), math.Min(qmin, q), math.Max(qmax, q)
				ylo, yhi = math.Min(ylo, p[1]), math.Max(yhi, p[1])
			}
			if smax < 0 || smin > l || qmax < -half || qmin > half {
				continue
			}
			for r := max(0, int(math.Ceil((ylo-rowLo)/rowStep))); r < rows; r++ {
				y := rowLo + float64(r)*rowStep
				if y > yhi {
					break
				}
				sg, ok := cutTri(t, y)
				if !ok {
					continue
				}
				s0, q0 := local(sg[0][0], sg[0][1])
				s1, q1 := local(sg[1][0], sg[1][1])
				// Clip to the slab |q| ≤ half.
				t0, t1 := 0.0, 1.0
				if dq := q1 - q0; math.Abs(dq) > 1e-9 {
					ta, tb := (-half-q0)/dq, (half-q0)/dq
					if ta > tb {
						ta, tb = tb, ta
					}
					t0, t1 = math.Max(t0, ta), math.Min(t1, tb)
				} else if math.Abs(q0) > half {
					continue
				}
				if t0 > t1 {
					continue
				}
				a, b := s0+(s1-s0)*t0, s0+(s1-s0)*t1
				if a > b {
					a, b = b, a
				}
				for k := max(0, int(a/binLen)); k <= min(nb-1, int(b/binLen)); k++ {
					cov[k][r] = true
				}
			}
		}

		// The floor at each bin: the higher of the two sides.
		floor := make([]float64, nb)
		for k := range floor {
			p := w.At((float64(k) + 0.5) * binLen)
			o := w.Thick/2 + 0.2
			floor[k] = math.Max(fm.at(p[0]+n[0]*o, p[1]+n[1]*o), fm.at(p[0]-n[0]*o, p[1]-n[1]*o))
		}
		row := func(k int, h float64) int {
			return min(rows-1, max(0, int(math.Round((floor[k]+h-rowLo)/rowStep))))
		}
		openShare := func(k int, h0, h1 float64) float64 {
			o, all := 0, 0
			for r := row(k, h0); r <= row(k, h1); r++ {
				all++
				if !cov[k][r] {
					o++
				}
			}
			return float64(o) / float64(all)
		}
		isDoor := make([]bool, nb)
		isWin := make([]bool, nb)
		for k := range nb {
			isDoor[k] = openShare(k, 0.25, 1.8) >= 0.85
			isWin[k] = !isDoor[k] && openShare(k, 1.0, 1.6) >= 0.8 && openShare(k, 0.1, 0.5) <= 0.5
		}
		for _, run := range runs(isDoor, 3) {
			width := float64(run[1]-run[0]) * binLen
			if width < 0.6 || width > 4 {
				continue
			}
			var heads []float64
			for k := run[0]; k < run[1]; k++ {
				r := row(k, 1.8)
				for r < rows-1 && !cov[k][r+1] {
					r++
				}
				heads = append(heads, rowLo+float64(r+1)*rowStep-floor[k])
			}
			at := (float64(run[0]+run[1]) / 2) * binLen
			at, width = fit(at, width, l)
			dr := home.Door{Wall: w.ID, At: round3(at), Width: round3(width), Kind: "swing", Hinge: "a", Height: round2(math.Min(median(heads), 2.6))}
			sideRoom := func(sign float64) *home.Room {
				p := w.At(at)
				for o := w.Thick/2 + 0.1; o <= w.Thick/2+0.5; o += 0.1 {
					if r := roomAt(rooms, home.P{p[0] + sign*n[0]*o, p[1] + sign*n[1]*o}); r != nil {
						return r
					}
				}
				return nil
			}
			pos, neg := sideRoom(1), sideRoom(-1)
			if pos == nil && neg == nil {
				continue
			}
			if !w.Exterior && (pos == nil || neg == nil) {
				continue // into no room: a wardrobe or an unscanned cupboard
			}
			if w.Exterior {
				in := pos
				if in == nil {
					in = neg
				}
				if in.Kind != "living" && in.Kind != "entry" && in.Kind != "balcony" {
					// Open to the floor in a bedroom, kitchen or bath: a hole in the scan,
					// not a door to the outside.
					continue
				}
			}
			switch {
			case dr.Height >= 2.4:
				dr.Kind, dr.Hinge = "opening", "" // up to the ceiling: no leaf
			case width >= 1.4 || isBalcony(pos) || isBalcony(neg):
				dr.Kind, dr.Hinge = "sliding", ""
			case pos == nil || neg == nil:
				dr.Side = 1 // into the home
				if pos == nil {
					dr.Side = -1
				}
				dr.Entry = w.Exterior
			default:
				dr.Side = 1 // into the smaller room
				if home.Area(neg.Poly) < home.Area(pos.Poly) {
					dr.Side = -1
				}
			}
			dr.Name = doorName(dr, pos, neg)
			doors = append(doors, dr)
		}
		for _, run := range runs(isWin, 3) {
			width := float64(run[1]-run[0]) * binLen
			if width < 0.35 {
				continue
			}
			var sills, heads []float64
			for k := run[0]; k < run[1]; k++ {
				lo := row(k, 1.3)
				for lo > 0 && !cov[k][lo-1] {
					lo--
				}
				hi := lo
				for hi < rows-1 && !cov[k][hi+1] {
					hi++
				}
				sills = append(sills, rowLo+float64(lo)*rowStep-floor[k])
				heads = append(heads, rowLo+float64(hi+1)*rowStep-floor[k])
			}
			at, width := fit(float64(run[0]+run[1])/2*binLen, width, l)
			sill := math.Max(0, median(sills))
			wins = append(wins, home.Window{Wall: w.ID, At: round3(at), Width: round3(width), Sill: round2(sill), Height: round2(math.Min(median(heads), 2.6) - sill)})
		}
	}
	doors = dedupeDoors(doors, ws)
	sort.SliceStable(doors, func(i, j int) bool { return doors[i].Wall < doors[j].Wall })
	for i := range doors {
		doors[i].ID = fmt.Sprintf("d%d", i+1)
	}
	for i := range wins {
		wins[i].ID = fmt.Sprintf("win%d", i+1)
	}
	return doors, wins
}

// fit keeps an opening inside its wall.
func fit(at, width, l float64) (float64, float64) {
	width = math.Min(width, l)
	at = math.Max(width/2, math.Min(l-width/2, at))
	return at, width
}

// runs returns [start, end) of the true stretches, bridging gaps of up to gap bins.
func runs(v []bool, gap int) [][2]int {
	var out [][2]int
	for k := 0; k < len(v); {
		if !v[k] {
			k++
			continue
		}
		s, e := k, k+1
		for e < len(v) {
			if v[e] {
				e++
				continue
			}
			j := e
			for j < len(v) && !v[j] && j-e < gap {
				j++
			}
			if j < len(v) && v[j] && j-e < gap {
				e = j
				continue
			}
			break
		}
		out = append(out, [2]int{s, e})
		k = e
	}
	return out
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func roomAt(rooms []home.Room, p home.P) *home.Room {
	for i := range rooms {
		if home.Inside(rooms[i].Poly, p) {
			return &rooms[i]
		}
	}
	return nil
}

func isBalcony(r *home.Room) bool { return r != nil && r.Kind == "balcony" }

// doorName is 卧室A门, 卧室A–客厅 and the like: the room the door belongs to by kind.
func doorName(d home.Door, a, b *home.Room) string {
	if d.Entry {
		return "入户门"
	}
	rank := map[string]int{"bath": 0, "kitchen": 1, "bedroom": 2, "balcony": 3, "entry": 4, "living": 5}
	var r *home.Room
	for _, c := range []*home.Room{a, b} {
		if c != nil && (r == nil || rank[c.Kind] < rank[r.Kind]) {
			r = c
		}
	}
	if r == nil {
		return ""
	}
	return r.Name + "门"
}

// dedupeDoors keeps one door where several walls side by side (a wall and a sliver of
// another) found the same opening: the one on the thickest wall.
func dedupeDoors(ds []home.Door, ws []home.Wall) []home.Door {
	wall := map[string]home.Wall{}
	for _, w := range ws {
		wall[w.ID] = w
	}
	centre := func(d home.Door) home.P { return wall[d.Wall].At(d.At) }
	var out []home.Door
	for _, d := range ds {
		c, dup := centre(d), false
		for i, o := range out {
			oc := centre(o)
			if math.Hypot(c[0]-oc[0], c[1]-oc[1]) < math.Max(d.Width, o.Width)/2+0.3 {
				dup = true
				if wall[d.Wall].Thick > wall[o.Wall].Thick {
					out[i] = d
				}
				break
			}
		}
		if !dup {
			out = append(out, d)
		}
	}
	return out
}
