package scan

import (
	"math"

	"hometwin/internal/home"
)

// squarePoly turns a traced staircase outline into a clean polygon: Douglas–Peucker at tol,
// then every edge within 30° of an axis snapped onto it, jogs shorter than minJog merged
// away, and the corners recomputed where the snapped edges meet.
func squarePoly(pts []home.P, tol, minJog float64) []home.P {
	return snapPoly(pts, tol, minJog, false)
}

// rectPoly is squarePoly with every edge put on an axis, slanted or not.
func rectPoly(pts []home.P, tol, minJog float64) []home.P {
	return snapPoly(pts, tol, minJog, true)
}

func snapPoly(pts []home.P, tol, minJog float64, axesOnly bool) []home.P {
	if len(pts) < 4 {
		return pts
	}
	p := simplifyClosed(pts, tol)
	if len(p) < 3 {
		return nil
	}
	type edge struct {
		kind int // 0 runs along x (z constant), 1 along z (x constant), 2 slanted
		c    float64
		a, b home.P
		w    float64 // length
	}
	mk := func(a, b home.P) edge {
		dx, dz := b[0]-a[0], b[1]-a[1]
		e := edge{a: a, b: b, w: math.Hypot(dx, dz)}
		ang := math.Abs(math.Atan2(dz, dx)) * 180 / math.Pi
		switch {
		case ang < 30 || ang > 150:
			e.kind, e.c = 0, (a[1]+b[1])/2
		case ang > 60 && ang < 120:
			e.kind, e.c = 1, (a[0]+b[0])/2
		case axesOnly && (ang < 45 || ang > 135):
			e.kind, e.c = 0, (a[1]+b[1])/2
		case axesOnly:
			e.kind, e.c = 1, (a[0]+b[0])/2
		default:
			e.kind = 2
		}
		return e
	}
	es := make([]edge, len(p))
	for i := range p {
		es[i] = mk(p[i], p[(i+1)%len(p)])
	}
	merge := func(x, y edge) edge {
		w := x.w + y.w
		return edge{kind: x.kind, c: (x.c*x.w + y.c*y.w) / w, a: x.a, b: y.b, w: w}
	}
	for changed := true; changed && len(es) > 3; {
		changed = false
		// Neighbours of the same axis become one edge.
		for i := 0; i < len(es) && len(es) > 3; i++ {
			j := (i + 1) % len(es)
			if es[i].kind != 2 && es[i].kind == es[j].kind {
				m := merge(es[i], es[j])
				es[i] = m
				es = append(es[:j], es[j+1:]...)
				changed = true
				i--
			}
		}
		// A short edge between two parallel ones is a jog: drop it, the parallels merge next
		// round. A short slanted edge is a cut corner or noise: drop it too.
		for i := 0; i < len(es) && len(es) > 4; i++ {
			if es[i].w >= minJog && !(es[i].kind == 2 && es[i].w < 2*minJog) {
				continue
			}
			pv, nx := es[(i+len(es)-1)%len(es)], es[(i+1)%len(es)]
			if (pv.kind != 2 && pv.kind == nx.kind) || es[i].kind == 2 {
				es = append(es[:i], es[i+1:]...)
				changed = true
				break
			}
		}
	}
	// Corners where consecutive edges meet.
	line := func(e edge) (home.P, home.P) { // point and direction
		switch e.kind {
		case 0:
			return home.P{e.a[0], e.c}, home.P{1, 0}
		case 1:
			return home.P{e.c, e.a[1]}, home.P{0, 1}
		}
		return e.a, home.P{e.b[0] - e.a[0], e.b[1] - e.a[1]}
	}
	out := make([]home.P, 0, len(es))
	for i := range es {
		p1, d1 := line(es[(i+len(es)-1)%len(es)])
		p2, d2 := line(es[i])
		den := d1[0]*d2[1] - d1[1]*d2[0]
		if math.Abs(den) < 1e-9 { // parallel neighbours (should not happen): keep the start
			out = append(out, es[i].a)
			continue
		}
		t := ((p2[0]-p1[0])*d2[1] - (p2[1]-p1[1])*d2[0]) / den
		out = append(out, home.P{round3(p1[0] + d1[0]*t), round3(p1[1] + d1[1]*t)})
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func simplifyClosed(p []home.P, tol float64) []home.P {
	// Split at the point farthest from p[0] and simplify both halves.
	far, fd := 0, -1.0
	for i, q := range p {
		if d := math.Hypot(q[0]-p[0][0], q[1]-p[0][1]); d > fd {
			far, fd = i, d
		}
	}
	a := dp(p[:far+1], tol)
	b := dp(append(append([]home.P{}, p[far:]...), p[0]), tol)
	return append(a[:len(a)-1], b[:len(b)-1]...)
}

func dp(p []home.P, tol float64) []home.P {
	if len(p) < 3 {
		return p
	}
	a, b := p[0], p[len(p)-1]
	idx, best := 0, -1.0
	for i := 1; i < len(p)-1; i++ {
		if d := segDist(p[i], a, b); d > best {
			idx, best = i, d
		}
	}
	if best <= tol {
		return []home.P{a, b}
	}
	l := dp(p[:idx+1], tol)
	r := dp(p[idx:], tol)
	return append(l[:len(l)-1], r...)
}

func segDist(p, a, b home.P) float64 {
	dx, dz := b[0]-a[0], b[1]-a[1]
	l2 := dx*dx + dz*dz
	t := 0.0
	if l2 > 0 {
		t = math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dz)/l2))
	}
	return math.Hypot(p[0]-a[0]-t*dx, p[1]-a[1]-t*dz)
}

// signedArea is positive for polygons running clockwise on screen (x right, z down).
func signedArea(p []home.P) float64 {
	s := 0.0
	for i := range p {
		a, b := p[i], p[(i+1)%len(p)]
		s += a[0]*b[1] - b[0]*a[1]
	}
	return s / 2
}
