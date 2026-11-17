package layout

import (
	"math"

	"hometwin/internal/home"
)

type P = home.P

// poly is a convex polygon on the floor plane.
type poly []P

func sub(a, b P) P            { return P{a[0] - b[0], a[1] - b[1]} }
func add(a, b P) P            { return P{a[0] + b[0], a[1] + b[1]} }
func mul(a P, k float64) P    { return P{a[0] * k, a[1] * k} }
func dot(a, b P) float64      { return a[0]*b[0] + a[1]*b[1] }
func length(a P) float64      { return math.Hypot(a[0], a[1]) }
func norm(a P) P              { l := length(a); return P{a[0] / l, a[1] / l} }
func perp(a P) P              { return P{-a[1], a[0]} }
func rad(deg float64) float64 { return deg * math.Pi / 180 }

// rotate turns a local offset by rot degrees the way three.js turns an object about +y:
// local +x goes to (cos, -sin), local +z to (sin, cos).
func rotate(lx, lz, rot float64) P {
	c, s := math.Cos(rad(rot)), math.Sin(rad(rot))
	return P{lx*c + lz*s, -lx*s + lz*c}
}

// rect is a w×d rectangle centred on (x, z), turned by rot degrees.
func rect(x, z, w, d, rot float64) poly {
	out := make(poly, 0, 4)
	for _, c := range [][2]float64{{-1, -1}, {1, -1}, {1, 1}, {-1, 1}} {
		out = append(out, add(P{x, z}, rotate(c[0]*w/2, c[1]*d/2, rot)))
	}
	return out
}

// along is the rectangle covering a wall from t0 to t1 metres along it, offset across it from
// o0 to o1 metres (0 is the centre line, + the normal side).
func along(w home.Wall, t0, t1, o0, o1 float64) poly {
	d, n := w.Dir(), w.Normal()
	at := func(t, o float64) P { return add(add(w.A, mul(d, t)), mul(n, o)) }
	return poly{at(t0, o0), at(t1, o0), at(t1, o1), at(t0, o1)}
}

func (p poly) project(axis P) (lo, hi float64) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, v := range p {
		x := dot(v, axis)
		lo, hi = math.Min(lo, x), math.Max(hi, x)
	}
	return
}

func (p poly) axes() []P {
	out := make([]P, 0, len(p))
	for i := range p {
		e := sub(p[(i+1)%len(p)], p[i])
		if length(e) > 1e-9 {
			out = append(out, norm(perp(e)))
		}
	}
	return out
}

// overlap is how deep two convex polygons run into each other (0 when they do not).
func overlap(a, b poly) float64 {
	depth := math.Inf(1)
	for _, ax := range append(a.axes(), b.axes()...) {
		alo, ahi := a.project(ax)
		blo, bhi := b.project(ax)
		d := math.Min(ahi, bhi) - math.Max(alo, blo)
		if d <= 0 {
			return 0
		}
		depth = math.Min(depth, d)
	}
	return depth
}

// meets says whether two convex polygons touch or cross; unlike overlap it works for a
// polygon with no area, such as a line.
func meets(a, b poly) bool {
	for _, ax := range append(a.axes(), b.axes()...) {
		alo, ahi := a.project(ax)
		blo, bhi := b.project(ax)
		if math.Min(ahi, bhi)-math.Max(alo, blo) < -1e-9 {
			return false
		}
	}
	return true
}

func segPoint(a, b, p P) P {
	ab := sub(b, a)
	l2 := dot(ab, ab)
	if l2 < 1e-12 {
		return a
	}
	t := math.Max(0, math.Min(1, dot(sub(p, a), ab)/l2))
	return add(a, mul(ab, t))
}

// gap is the distance between two convex polygons that do not overlap, and the closest pair
// of points (on a, on b).
func gap(a, b poly) (float64, P, P) {
	best, pa, pb := math.Inf(1), P{}, P{}
	try := func(p, s0, s1 P, pOnA bool) {
		q := segPoint(s0, s1, p)
		if d := length(sub(p, q)); d < best {
			best = d
			if pOnA {
				pa, pb = p, q
			} else {
				pa, pb = q, p
			}
		}
	}
	for i := range a {
		for j := range b {
			try(a[i], b[j], b[(j+1)%len(b)], true)
			try(b[j], a[i], a[(i+1)%len(a)], false)
		}
	}
	return best, pa, pb
}

// facing is how long two polygons face each other across the gap between them: the overlap of
// their shadows on the line across the gap's direction.
func facing(a, b poly, pa, pb P) float64 {
	d := sub(pb, pa)
	if length(d) < 1e-9 {
		return 0
	}
	t := perp(norm(d))
	alo, ahi := a.project(t)
	blo, bhi := b.project(t)
	return math.Min(ahi, bhi) - math.Max(alo, blo)
}

func centroid(p poly) P {
	c := P{}
	for _, v := range p {
		c = add(c, v)
	}
	return mul(c, 1/float64(len(p)))
}
