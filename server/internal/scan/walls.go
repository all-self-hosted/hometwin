package scan

import (
	"fmt"
	"math"
	"sort"

	"hometwin/internal/home"
)

const (
	// Faces of two rooms this far apart, facing each other, are the two sides of one wall.
	minThick, maxThick = 0.04, 0.45
	interiorThick      = 0.12 // a wall whose far side the scan did not see
)

// piece is a straight wall: along x at z = c (axis 0), along z at x = c (axis 1), or
// slanted from a to b (axis 2); s0..s1 is its extent along the axis.
type piece struct {
	axis   int
	c      float64
	s0, s1 float64
	a, b   home.P
	thick  float64
	ext    bool
}

type roomEdge struct {
	room   int
	axis   int
	c      float64
	s0, s1 float64
	out    float64 // +1 or −1: which way along the other axis is outside the room
	a, b   home.P
}

func edgesOf(rooms []home.Room) []roomEdge {
	var es []roomEdge
	for i, r := range rooms {
		p := r.Poly
		for k := range p {
			a, b := p[k], p[(k+1)%len(p)]
			e := roomEdge{room: i, a: a, b: b}
			mid := home.P{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2}
			switch {
			case math.Abs(a[1]-b[1]) < 1e-6:
				e.axis, e.c, e.s0, e.s1 = 0, a[1], math.Min(a[0], b[0]), math.Max(a[0], b[0])
				e.out = 1
				if home.Inside(p, home.P{mid[0], mid[1] + 0.02}) {
					e.out = -1
				}
			case math.Abs(a[0]-b[0]) < 1e-6:
				e.axis, e.c, e.s0, e.s1 = 1, a[0], math.Min(a[1], b[1]), math.Max(a[1], b[1])
				e.out = 1
				if home.Inside(p, home.P{mid[0] + 0.02, mid[1]}) {
					e.out = -1
				}
			default:
				e.axis = 2
			}
			es = append(es, e)
		}
	}
	return es
}

// walls puts a wall along every room edge: one wall, as thick as the gap, where two rooms
// face each other across a gap of minThick..maxThick; an outer wall of ExteriorThick where
// nothing is on the other side; a thin wall where a room is there but further away.
func walls(rooms []home.Room) []home.Wall {
	es := edgesOf(rooms)
	covered := make([][][2]float64, len(es))
	var ps []piece
	for i, e := range es {
		if e.axis == 2 {
			continue
		}
		for j := i + 1; j < len(es); j++ {
			f := es[j]
			if f.room == e.room || f.axis != e.axis || f.out != -e.out {
				continue
			}
			gap := (f.c - e.c) * e.out
			lo, hi := math.Max(e.s0, f.s0), math.Min(e.s1, f.s1)
			if gap < minThick || gap > maxThick || hi-lo < 0.05 {
				continue
			}
			ps = append(ps, piece{axis: e.axis, c: (e.c + f.c) / 2, s0: lo, s1: hi, thick: gap})
			covered[i] = append(covered[i], [2]float64{lo, hi})
			covered[j] = append(covered[j], [2]float64{lo, hi})
		}
	}
	inAnother := func(room int, p home.P) bool {
		for k, r := range rooms {
			if k != room && home.Inside(r.Poly, p) {
				return true
			}
		}
		return false
	}
	for i, e := range es {
		if e.axis == 2 {
			// A slanted edge: a wall along it, pushed out by half its thickness.
			d := home.P{e.b[0] - e.a[0], e.b[1] - e.a[1]}
			l := math.Hypot(d[0], d[1])
			n := home.P{-d[1] / l, d[0] / l}
			mid := home.P{(e.a[0] + e.b[0]) / 2, (e.a[1] + e.b[1]) / 2}
			if home.Inside(rooms[e.room].Poly, home.P{mid[0] + n[0]*0.02, mid[1] + n[1]*0.02}) {
				n = home.P{-n[0], -n[1]}
			}
			t, ext := interiorThick, !inAnother(e.room, home.P{mid[0] + n[0]*0.5, mid[1] + n[1]*0.5})
			if ext {
				t = ExteriorThick
			}
			off := home.P{n[0] * t / 2, n[1] * t / 2}
			ps = append(ps, piece{axis: 2, a: home.P{e.a[0] + off[0], e.a[1] + off[1]}, b: home.P{e.b[0] + off[0], e.b[1] + off[1]}, thick: t, ext: ext})
			continue
		}
		for _, iv := range uncovered(e.s0, e.s1, covered[i]) {
			if iv[1]-iv[0] < 0.08 {
				continue
			}
			m := (iv[0] + iv[1]) / 2
			probe := func(d float64) home.P {
				if e.axis == 0 {
					return home.P{m, e.c + e.out*d}
				}
				return home.P{e.c + e.out*d, m}
			}
			t, ext := interiorThick, true
			for _, d := range []float64{0.5, 0.8} {
				if inAnother(e.room, probe(d)) {
					ext = false
				}
			}
			if ext {
				t = ExteriorThick
			}
			ps = append(ps, piece{axis: e.axis, c: e.c + e.out*t/2, s0: iv[0], s1: iv[1], thick: t, ext: ext})
		}
	}
	ps = mergePieces(ps)
	out := make([]home.Wall, 0, len(ps))
	for _, p := range ps {
		if p.axis != 2 && p.s1-p.s0 < 0.08 {
			continue
		}
		w := home.Wall{ID: fmt.Sprintf("w%d", len(out)+1), Thick: round3(p.thick), Exterior: p.ext}
		switch p.axis {
		case 0:
			w.A, w.B = home.P{round3(p.s0), round3(p.c)}, home.P{round3(p.s1), round3(p.c)}
		case 1:
			w.A, w.B = home.P{round3(p.c), round3(p.s0)}, home.P{round3(p.c), round3(p.s1)}
		default:
			w.A, w.B = home.P{round3(p.a[0]), round3(p.a[1])}, home.P{round3(p.b[0]), round3(p.b[1])}
		}
		out = append(out, w)
	}
	return out
}

// uncovered is [s0, s1] less the intervals.
func uncovered(s0, s1 float64, ivs [][2]float64) [][2]float64 {
	sort.Slice(ivs, func(i, j int) bool { return ivs[i][0] < ivs[j][0] })
	var out [][2]float64
	at := s0
	for _, iv := range ivs {
		if iv[0] > at {
			out = append(out, [2]float64{at, math.Min(iv[0], s1)})
		}
		at = math.Max(at, iv[1])
	}
	if at < s1 {
		out = append(out, [2]float64{at, s1})
	}
	return out
}

// mergePieces joins pieces on one line that touch and are about as thick, and sorts them.
func mergePieces(ps []piece) []piece {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].axis != ps[j].axis {
			return ps[i].axis < ps[j].axis
		}
		if math.Abs(ps[i].c-ps[j].c) > 0.03 {
			return ps[i].c < ps[j].c
		}
		return ps[i].s0 < ps[j].s0
	})
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(ps) && !changed; i++ {
			for j := 0; j < len(ps) && !changed; j++ {
				a, b := ps[i], ps[j]
				if i == j || a.axis == 2 || a.axis != b.axis || math.Abs(a.c-b.c) > 0.03 ||
					math.Abs(a.thick-b.thick) > 0.05 || a.ext != b.ext || b.s0 < a.s0 || b.s0 > a.s1+0.03 {
					continue
				}
				la, lb := a.s1-a.s0, b.s1-b.s0
				a.c = (a.c*la + b.c*lb) / (la + lb)
				a.thick = (a.thick*la + b.thick*lb) / (la + lb)
				a.s1 = math.Max(a.s1, b.s1)
				ps[i] = a
				ps = append(ps[:j], ps[j+1:]...)
				changed = true
			}
		}
	}
	return ps
}

// extendEnds runs each wall on to the far face of a wall it meets end-on, so the corners
// are filled in.
func extendEnds(ws []home.Wall) []home.Wall {
	axis := func(w home.Wall) int {
		switch {
		case w.A[1] == w.B[1]:
			return 0
		case w.A[0] == w.B[0]:
			return 1
		}
		return 2
	}
	out := append([]home.Wall(nil), ws...)
	for i, w := range ws {
		ax := axis(w)
		if ax == 2 {
			continue
		}
		c := w.A[1-ax]
		s0, s1 := w.A[ax], w.B[ax]
		for _, v := range ws {
			if axis(v) != 1-ax {
				continue
			}
			vc := v.A[ax]
			v0, v1 := math.Min(v.A[1-ax], v.B[1-ax]), math.Max(v.A[1-ax], v.B[1-ax])
			if c < v0-w.Thick/2-0.05 || c > v1+w.Thick/2+0.05 {
				continue
			}
			reach := v.Thick/2 + 0.05
			if math.Abs(vc-s0) <= reach {
				s0 = math.Min(s0, vc-v.Thick/2)
			}
			if math.Abs(vc-s1) <= reach {
				s1 = math.Max(s1, vc+v.Thick/2)
			}
		}
		out[i].A[ax], out[i].B[ax] = round3(s0), round3(s1)
	}
	return out
}
