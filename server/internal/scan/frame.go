package scan

import (
	"errors"
	"math"
	"sort"
)

// Frame maps the scan's coordinates (metres, z up) into HomeTwin's (x east, y up, z south):
// level the walls onto the axes, put the main floor at y = 0, turn north to −z and move the
// north-west corner to the origin.
type Frame struct {
	// Angle (radians) is how far the scan's walls are turned off its X axis, within ±45°.
	Angle float64 `json:"angle"`
	// Turns is the number of quarter turns, clockwise seen from above, that bring north to −z
	// after levelling. With 0 the scan's X runs east and its Y north.
	Turns int `json:"turns"`
	// Floor is the scan z of the main floor; Ceiling its ceiling height above that floor.
	Floor   float64 `json:"floor"`
	Ceiling float64 `json:"ceiling"`
	// Origin is subtracted from x and z after turning.
	Origin [2]float64 `json:"origin"`
}

// ExteriorThick is used for outer walls, whose outside a scan does not see.
const ExteriorThick = 0.24

// Point is a scan point in the home's frame.
func (f Frame) Point(x, y, z float64) [3]float64 {
	c, s := math.Cos(f.Angle), math.Sin(f.Angle)
	x, y = x*c+y*s, -x*s+y*c
	hx, hz := x, -y // z up → y up
	for range (f.Turns%4 + 4) % 4 {
		hx, hz = -hz, hx
	}
	return [3]float64{hx - f.Origin[0], z - f.Floor, hz - f.Origin[1]}
}

// Orient finds a scan's frame: the wall direction from the vertical faces, the main floor
// and ceiling from the horizontal ones, and the origin from the outline 1.2 m above the
// floor. turns says where north is (see Frame.Turns); the scan does not know.
func Orient(m Mesh, turns int) (Frame, error) {
	f := Frame{Turns: turns}
	var angW [360]float64 // quarter-degree bins of the wall normals' angle mod 90°
	type hz struct{ z, a float64 }
	var up, down []hz
	eachTri(m, func(a, b, c [3]float64) {
		n := cross(sub(b, a), sub(c, a))
		l := norm(n)
		if l == 0 {
			return
		}
		area := l / 2
		nz := n[2] / l
		switch {
		case math.Abs(nz) < 0.2:
			deg := math.Mod(math.Atan2(n[1], n[0])*180/math.Pi+360, 90)
			angW[int(deg*4)%360] += area
		case nz > 0.95:
			up = append(up, hz{(a[2] + b[2] + c[2]) / 3, area})
		case nz < -0.95:
			down = append(down, hz{(a[2] + b[2] + c[2]) / 3, area})
		}
	})
	if len(up) == 0 || len(down) == 0 {
		return f, errors.New("扫描里找不到地面或天花板")
	}
	// Wall angle: the heaviest bin, refined by a circular mean over ±2° around it.
	best := 0
	for i := range angW {
		if angW[i] > angW[best] {
			best = i
		}
	}
	var sx, sy float64
	for d := -8; d <= 8; d++ {
		i := (best + d + 360) % 360
		th := (float64(i) + 0.5) / 4 * math.Pi / 180 * 4 // ×4 maps the 90° period onto a circle
		sx += angW[i] * math.Cos(th)
		sy += angW[i] * math.Sin(th)
	}
	f.Angle = math.Atan2(sy, sx) / 4
	if f.Angle > math.Pi/4 {
		f.Angle -= math.Pi / 2
	}

	peak := func(v []hz, lo, hi float64) float64 {
		const bin = 0.01
		n := int((hi-lo)/bin) + 1
		h := make([]float64, n)
		for _, p := range v {
			if p.z >= lo && p.z < hi {
				h[int((p.z-lo)/bin)] += p.a
			}
		}
		bi := 0
		for i := range h {
			if h[i] > h[bi] {
				bi = i
			}
		}
		c := lo + (float64(bi)+0.5)*bin
		var s, w float64
		for _, p := range v {
			if math.Abs(p.z-c) <= 0.015 {
				s += p.z * p.a
				w += p.a
			}
		}
		return s / w
	}
	zmin, zmax := math.Inf(1), math.Inf(-1)
	for _, p := range up {
		zmin = math.Min(zmin, p.z)
		zmax = math.Max(zmax, p.z)
	}
	f.Floor = peak(up, zmin, zmax+0.01)
	f.Ceiling = peak(down, f.Floor+1.9, f.Floor+4.5) - f.Floor
	if math.IsNaN(f.Ceiling) {
		return f, errors.New("扫描里找不到天花板")
	}

	// Origin: the inner faces of the outer walls at 1.2 m, ignoring the odd stray point,
	// less half an outer wall so the wall centre lines run through 0 as in sample.json.
	var xs, zs []float64
	for _, s := range sliceAt(worldTris(m, f), func(float64, float64) float64 { return 1.2 }) {
		xs = append(xs, s[0][0], s[1][0])
		zs = append(zs, s[0][1], s[1][1])
	}
	if len(xs) == 0 {
		return f, errors.New("扫描里找不到墙")
	}
	f.Origin = [2]float64{quantile(xs, 0.003) - ExteriorThick/2, quantile(zs, 0.003) - ExteriorThick/2}
	return f, nil
}

func quantile(v []float64, q float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[int(q*float64(len(s)-1))]
}

// tri is a triangle in the home's frame with its unit normal and area, and where its
// texture is: the chunk and the texture coordinates of its centre.
type tri struct {
	p     [3][3]float64
	n     [3]float64
	area  float64
	chunk int
	uv    [2]float32    // at the centre
	uvs   [3][2]float32 // at the corners
}

func (t tri) centre() [3]float64 {
	return [3]float64{(t.p[0][0] + t.p[1][0] + t.p[2][0]) / 3, (t.p[0][1] + t.p[1][1] + t.p[2][1]) / 3, (t.p[0][2] + t.p[1][2] + t.p[2][2]) / 3}
}

func worldTris(m Mesh, f Frame) []tri {
	out := make([]tri, 0, m.Triangles())
	for ci, ch := range m.Chunks {
		for i := 0; i+2 < len(ch.Index); i += 3 {
			t := tri{chunk: ci}
			for j := range 3 {
				v := ch.Index[i+j]
				t.p[j] = f.Point(float64(ch.Pos[3*v]), float64(ch.Pos[3*v+1]), float64(ch.Pos[3*v+2]))
				if len(ch.UV) > 0 {
					t.uvs[j] = [2]float32{ch.UV[2*v], ch.UV[2*v+1]}
					t.uv[0] += ch.UV[2*v] / 3
					t.uv[1] += ch.UV[2*v+1] / 3
				}
			}
			n := cross(sub(t.p[1], t.p[0]), sub(t.p[2], t.p[0]))
			l := norm(n)
			if l == 0 {
				continue
			}
			t.n = [3]float64{n[0] / l, n[1] / l, n[2] / l}
			t.area = l / 2
			out = append(out, t)
		}
	}
	return out
}

func eachTri(m Mesh, fn func(a, b, c [3]float64)) {
	for _, ch := range m.Chunks {
		v := func(i uint32) [3]float64 {
			return [3]float64{float64(ch.Pos[3*i]), float64(ch.Pos[3*i+1]), float64(ch.Pos[3*i+2])}
		}
		for i := 0; i+2 < len(ch.Index); i += 3 {
			fn(v(ch.Index[i]), v(ch.Index[i+1]), v(ch.Index[i+2]))
		}
	}
}

func sub(a, b [3]float64) [3]float64 { return [3]float64{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func cross(a, b [3]float64) [3]float64 {
	return [3]float64{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func norm(a [3]float64) float64 { return math.Sqrt(a[0]*a[0] + a[1]*a[1] + a[2]*a[2]) }

// seg is a segment on the floor plane (x, z).
type seg [2][2]float64

// sliceAt cuts the triangles with the surface y = h(x, z), h taken at each triangle's centre,
// and returns the cut lines on the floor plane. Flat triangles are skipped.
func sliceAt(ts []tri, h func(x, z float64) float64) []seg {
	var out []seg
	for _, t := range ts {
		if math.Abs(t.n[1]) > 0.9 {
			continue
		}
		c := t.centre()
		y := h(c[0], c[2])
		if s, ok := cutTri(t, y); ok {
			out = append(out, s)
		}
	}
	return out
}

func cutTri(t tri, y float64) (seg, bool) {
	var pts [][2]float64
	for i := range 3 {
		a, b := t.p[i], t.p[(i+1)%3]
		da, db := a[1]-y, b[1]-y
		if (da < 0) != (db < 0) {
			k := da / (da - db)
			pts = append(pts, [2]float64{a[0] + (b[0]-a[0])*k, a[2] + (b[2]-a[2])*k})
		}
	}
	if len(pts) != 2 {
		return seg{}, false
	}
	return seg{pts[0], pts[1]}, true
}
